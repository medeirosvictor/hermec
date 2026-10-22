package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/names"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/discover"
)

const nonceSize = 32

// conn is one client connection. One goroutine (readLoop) reads and owns the
// auth state; one goroutine (writeLoop) writes everything queued on send.
type conn struct {
	srv *Server
	ws  *websocket.Conn

	sendMu sync.Mutex
	send   chan []byte // closed (under sendMu) when the connection ends
	closed bool

	ip           string // remote IP
	leavePreAuth func() // releases the pre-auth slot; idempotent, readLoop only
	counted      bool   // holds a MaxClients slot; readLoop only

	// Set once authenticated; only touched by readLoop.
	fingerprint string
	name        string

	voiceCh  string // current voice channel, "" if none; guarded by srv.chMu
	sharing  bool   // sharing a screen in voiceCh; guarded by srv.chMu
	watching string // fingerprint of the sharer being watched, "" if none; guarded by srv.chMu
	watchGen uint32 // gen of the watch request that set watching (echoed in video_source); guarded by srv.chMu
}

func newConn(s *Server, ws *websocket.Conn) *conn {
	return &conn{srv: s, ws: ws, send: make(chan []byte, sendBuffer)}
}

// enqueue queues an encoded message for the writer. It reports false if the
// connection is closed or its outbound buffer is full (slow consumer).
func (c *conn) enqueue(raw []byte) bool {
	if len(raw) > proto.MaxMessageSize {
		// Clients refuse frames this large (and would drop us); one oversize
		// frame must not take its recipient down, so drop it and report ok.
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &head)
		c.srv.logf("server: dropping %d-byte %q frame for %s (max %d)", len(raw), head.Type, c.fingerprint, proto.MaxMessageSize)
		return true
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- raw:
		return true
	default:
		return false
	}
}

func (c *conn) sendMsg(msgType string, payload any) bool {
	raw, err := proto.Encode(msgType, payload)
	if err != nil {
		return false
	}
	return c.enqueue(raw)
}

func (c *conn) sendError(code, message string) {
	c.sendMsg(proto.TypeError, proto.ErrorMsg{Code: code, Message: message})
}

// sendErrorGen is sendError for a refused watch: it echoes the request's gen.
func (c *conn) sendErrorGen(code, message string, gen uint32) {
	c.sendMsg(proto.TypeError, proto.ErrorMsg{Code: code, Message: message, Gen: gen})
}

// finish stops accepting outbound messages; the writer drains what is already
// queued (e.g. a final error), then closes the socket.
func (c *conn) finish() {
	c.srv.unregister(c)
	if c.counted {
		c.counted = false
		c.srv.releaseClient()
	}
	c.srv.leaveAll(c)
	c.closeSend()
}

// closeSend stops accepting outbound messages; safe from any goroutine and
// idempotent. The writer drains the queue and closes the socket, which ends the
// read loop.
func (c *conn) closeSend() {
	c.sendMu.Lock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
	c.sendMu.Unlock()
}

func (c *conn) writeLoop() {
	defer c.srv.wg.Done()
	defer c.ws.Close()
	for raw := range c.send {
		_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := c.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
			return
		}
	}
}

func (c *conn) readLoop() {
	defer c.srv.wg.Done()
	defer c.finish()

	err := c.authenticate()
	if c.leavePreAuth != nil {
		c.leavePreAuth()
	}
	if err != nil {
		return
	}
	_ = c.ws.SetReadDeadline(time.Time{})
	limit := newBucket(c.srv.cfg.MsgRate, c.srv.cfg.MsgBurst, c.srv.clock())

	var missed atomic.Int32
	c.ws.SetPongHandler(func(string) error { missed.Store(0); return nil })
	done := make(chan struct{})
	defer close(done)
	c.srv.wg.Add(1)
	go func() {
		defer c.srv.wg.Done()
		c.keepalive(done, &missed)
	}()

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		// Only data messages reach here; ping/pong/close are control frames
		// handled inside gorilla and are not counted.
		if !limit.allow(c.srv.clock()) {
			c.sendError("rate_limited", "too many messages; slow down")
			c.drainInbound()
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			c.sendError(proto.CodeBadRequest, "malformed message")
			continue
		}
		c.route(env)
	}
}

// drainInbound discards incoming frames for a short, fixed window after a
// rate-limit violation. Closing a socket that still holds unread inbound data
// makes Linux send RST, which can discard the client's not-yet-read
// rate_limited frame; draining lets the queued error flush first. Delivery
// remains best-effort (a spammer outpacing the window can still be reset).
func (c *conn) drainInbound() {
	_ = c.ws.SetReadDeadline(time.Now().Add(rateLimitDrain))
	for {
		if _, _, err := c.ws.ReadMessage(); err != nil {
			return
		}
	}
}

const rateLimitDrain = 200 * time.Millisecond

var (
	errAuthFailed   = errors.New("auth failed")
	errBadHandshake = errors.New("malformed handshake message")
)

// joinLive registers c for broadcasts and sends it the chain entries after
// seq, the head the handshake sent, so its copy has no gap.
func (c *conn) joinLive(seq uint64) error {
	s := c.srv
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	s.markAuthed(c)
	missed := s.chain.Since(seq)
	if len(missed) == 0 {
		return nil
	}
	frames, err := chainFrames(missed)
	if err != nil {
		return err
	}
	for _, f := range frames {
		if !c.enqueue(f) {
			return errAuthFailed
		}
	}
	return nil
}

// authenticate runs the v2 mutual handshake: the client asks the host to prove
// itself (host_proof_req), the host sends its chain and a signature over both
// nonces and the server ID, then the client authenticates (auth). On failure
// it queues an error and returns it; the connection is then closed.
func (c *conn) authenticate() error {
	pre := newBucket(preAuthMsgRate, preAuthMsgBurst, c.srv.clock())
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	// The challenge stays a v1 envelope so an old client can still read it
	// and be told to update.
	chRaw, err := proto.EncodeV(proto.LegacyVersion, proto.TypeChallenge, proto.Challenge{Nonce: nonce})
	if err != nil || !c.enqueue(chRaw) {
		return errAuthFailed
	}

	_ = c.ws.SetReadDeadline(time.Now().Add(authTimeout))

	// Pre-auth bucket: fixed, small, never disabled. The handshake reads two
	// messages (host_proof_req, auth); it bounds the exchange and a flood is
	// otherwise cut by auth_failed.
	readPre := func() (proto.Envelope, error) {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return proto.Envelope{}, err
		}
		if !pre.allow(c.srv.clock()) {
			c.sendError("rate_limited", "too many messages; slow down")
			return proto.Envelope{}, errAuthFailed
		}
		env, err := proto.DecodeHandshake(raw)
		if err != nil {
			return proto.Envelope{}, errBadHandshake
		}
		return env, nil
	}

	fail := func() error {
		c.srv.noteAuthFailure(c.ip) // blocks further connects from this IP; the reply is not delayed
		c.sendError("auth_failed", "authentication failed")
		return errAuthFailed
	}

	env, err := readPre()
	if errors.Is(err, errBadHandshake) {
		return fail()
	}
	if err != nil {
		return err // read error, or rate limited (already answered)
	}
	if env.V == proto.LegacyVersion {
		// A v1 client sends auth straight after the challenge. Tell a
		// well-formed one to update; anything else is an ordinary failure.
		var old proto.Auth
		if env.Type != proto.TypeAuth || json.Unmarshal(env.Data, &old) != nil {
			return fail()
		}
		if raw, err := proto.EncodeV(proto.LegacyVersion, proto.TypeError, proto.ErrorMsg{
			Code: proto.CodeUpdateRequired, Message: "update HERMEC to connect to this server",
		}); err == nil {
			c.enqueue(raw)
		}
		return errAuthFailed
	}
	if env.Type != proto.TypeHostProofReq {
		return fail()
	}
	var req proto.HostProofReq
	if err := json.Unmarshal(env.Data, &req); err != nil || len(req.Nonce) != nonceSize {
		return fail()
	}

	// Prove this host to the client before it sends anything secret.
	id := c.srv.chain.ID()
	sent := c.srv.chain.Entries()
	sentSeq := sent[len(sent)-1].Seq
	frames, err := chainFrames(sent)
	if err != nil {
		return err
	}
	for _, f := range frames {
		if !c.enqueue(f) {
			return errAuthFailed
		}
	}
	if !c.sendMsg(proto.TypeHostProof, proto.HostProof{
		PubKey: c.srv.cfg.HostKey.PublicKey(),
		Sig:    c.srv.cfg.HostKey.Sign(proto.HostProofBytes(req.Nonce, nonce, id[:])),
	}) {
		return errAuthFailed
	}

	env, err = readPre()
	if errors.Is(err, errBadHandshake) {
		return fail()
	}
	if err != nil {
		return err
	}
	if env.V != proto.Version || env.Type != proto.TypeAuth {
		return fail()
	}
	var auth proto.Auth
	if err := json.Unmarshal(env.Data, &auth); err != nil {
		return fail()
	}
	if len(auth.PubKey) != ed25519.PublicKeySize {
		return fail()
	}
	pub := ed25519.PublicKey(auth.PubKey)
	if !identity.Verify(pub, proto.ClientAuthBytes(nonce, req.Nonce, id[:]), auth.Sig) {
		return fail()
	}
	if c.srv.banned(identity.Fingerprint(pub)) {
		// The key is proven, so telling it is safe; not an auth failure.
		c.sendError(proto.CodeBanned, "you are banned from this server")
		return errAuthFailed
	}
	if pw := c.srv.cfg.Password; pw != "" &&
		subtle.ConstantTimeCompare([]byte(pw), []byte(auth.Password)) != 1 {
		return fail()
	}
	fp := identity.Fingerprint(pub)
	if !c.srv.keyAllowed(fp) {
		return fail()
	}

	c.srv.noteAuthSuccess(c.ip)
	if !c.srv.reserveClient() {
		c.sendError(proto.CodeServerFull, "server is full")
		return errAuthFailed
	}
	c.counted = true

	c.fingerprint = fp
	c.name = names.Clean(auth.Name, discover.MaxNameRunes)
	if c.name == "" {
		c.name = "anon"
	}
	c.sendMsg(proto.TypeAuthOK, proto.AuthOK{
		Fingerprint: fp,
		Roles:       c.srv.roles().RolesFor(fp),
		Channels:    c.srv.channelInfos(),
		ServerName:  c.srv.cfg.ServerName,
		ServerID:    id.String(),
	})
	// Registered after auth_ok is queued so voice_state never precedes it.
	// Under chainMu, so no append can broadcast in between: entries added
	// since the handshake sent the chain are caught up here, and every later
	// one reaches this connection through the broadcast.
	if err := c.joinLive(sentSeq); err != nil {
		return err
	}
	// A ban appended between the check above and markAuthed missed this
	// connection (kick only reaches authed ones).
	if c.srv.banned(fp) {
		c.sendError(proto.CodeBanned, "you are banned from this server")
		return errAuthFailed
	}
	return nil
}
