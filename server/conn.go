package server

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/names"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/internal/ratelimit"
	"github.com/medeirosvictor/hermec/version"
)

const nonceSize = 32

// passwordKey is the key clients must prove they know. nil means no password
// admission is configured.
func (s *Server) passwordKey(serverID []byte) []byte {
	key, _ := s.passwordState(serverID)
	return key
}

// passwordState returns the password key and whether password admission is
// required, from one snapshot of the mirrored key.
func (s *Server) passwordState(serverID []byte) (key []byte, set bool) {
	s.pwMu.RLock()
	mirrored, mkey := s.pwMirrored, s.pwMirror
	s.pwMu.RUnlock()
	if mirrored {
		return mkey, len(mkey) > 0
	}
	if len(s.cfg.PasswordKey) > 0 {
		return s.cfg.PasswordKey, true
	}
	return nil, false
}

// setPasswordKey makes k, the active host's password key, the one clients must
// prove (nil: no password), replacing whatever the config says.
func (s *Server) setPasswordKey(k []byte) {
	s.pwMu.Lock()
	old, _ := s.passwordStateLocked()
	s.pwMirrored, s.pwMirror = true, append([]byte(nil), k...)
	s.pwMu.Unlock()
	s.keyChanged(old, k)
}

// passwordStateLocked is the key now in force, for comparing; pwMu is held.
func (s *Server) passwordStateLocked() ([]byte, bool) {
	if s.pwMirrored {
		return s.pwMirror, len(s.pwMirror) > 0
	}
	return s.cfg.PasswordKey, len(s.cfg.PasswordKey) > 0
}

// checkPassword reports whether auth satisfies password admission. With no
// password key configured everything passes.
func (s *Server) checkPassword(auth proto.Auth, serverNonce, clientNonce, serverID []byte) bool {
	key, set := s.passwordState(serverID)
	if !set {
		return true
	}
	if len(key) == 0 {
		return false
	}
	return hmac.Equal(auth.Proof, proto.PasswordProof(key, serverNonce, clientNonce, serverID))
}

// conn is one client connection. One goroutine (readLoop) reads and owns the
// auth state; one goroutine (writeLoop) writes everything queued on send.
type conn struct {
	srv *Server
	ws  *websocket.Conn

	sendMu  sync.Mutex
	send    chan []byte // closed (under sendMu) when the connection ends
	closed  bool
	drained chan struct{} // signaled (cap 1) when the writer took a message or send closed; wakes enqueueWait

	ip           string      // remote IP
	leavePreAuth func()      // releases the pre-auth slot; idempotent, readLoop only
	counted      atomic.Bool // holds a MaxClients slot; promotion sets it from another goroutine
	warmHeld     atomic.Bool // holds a slot of the warm cap

	// Set once authenticated; only touched by readLoop.
	fingerprint string
	name        string
	pub         []byte          // the client's public key
	warm        bool            // authenticated with Auth.Warm
	link        *linkState      // non-nil once a host link_hello was accepted; set by readLoop
	reach       map[string]bool // host fingerprint -> client reported it reachable; guarded by srv.chMu

	voiceCh       string                                // current voice channel, "" if none; guarded by srv.chMu
	published     map[proto.MediaKind]bool              // active local lanes; guarded by srv.chMu
	subscriptions map[proto.MediaKind]mediaSubscription // selected sources; guarded by srv.chMu
}

func newConn(s *Server, ws *websocket.Conn) *conn {
	return &conn{srv: s, ws: ws, send: make(chan []byte, sendBuffer), drained: make(chan struct{}, 1)}
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

// sendMandatory queues a protocol-critical frame. Unlike best-effort updates,
// an oversized mandatory frame is a failed handshake, never a silent drop.
func (c *conn) sendMandatory(msgType string, payload any) bool {
	raw, err := proto.Encode(msgType, payload)
	if err != nil || len(raw) > proto.MaxMessageSize {
		return false
	}
	return c.enqueue(raw)
}

func (c *conn) sendError(code, message string) {
	c.sendMsg(proto.TypeError, proto.ErrorMsg{Code: code, Message: message})
}

// sendErrorRef is sendError for a failed chain_append: it echoes the
// request's ref.
func (c *conn) sendErrorRef(code, message, ref string) {
	c.sendMsg(proto.TypeError, proto.ErrorMsg{Code: code, Message: message, Ref: ref})
}

// sendErrorGen is sendError for a refused media request: it echoes the request's gen.
func (c *conn) sendErrorGen(code, message string, gen uint32) {
	c.sendMsg(proto.TypeError, proto.ErrorMsg{Code: code, Message: message, Gen: gen})
}

// finish stops accepting outbound messages; the writer drains what is already
// queued (e.g. a final error), then closes the socket.
func (c *conn) finish() {
	c.srv.unregister(c)
	if c.counted.Swap(false) {
		c.srv.releaseClient()
	}
	if c.warmHeld.Swap(false) {
		c.srv.releaseWarmSlot()
	}
	c.srv.closeWarmPeer(c)
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
	signal(c.drained)
}

func (c *conn) writeLoop() {
	defer c.srv.wg.Done()
	defer c.ws.Close()
	for raw := range c.send {
		signal(c.drained)
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
	limit := ratelimit.NewBucket(c.srv.cfg.MsgRate, c.srv.cfg.MsgBurst, c.srv.clock())

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
		if !limit.Allow(c.srv.clock()) {
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

// refuseBuild answers a peer running another build with version_mismatch and
// reports whether it did; every peer must run this server's build.
func (c *conn) refuseBuild(build string) bool {
	if build == version.Version {
		return false
	}
	c.srv.logf("server: refused %s: runs build %q, this server runs %q", c.ip, build, version.Version)
	c.sendError(proto.CodeVersionMismatch, proto.VersionMismatchMessage(version.Version, build))
	return true
}

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
	if c.warm {
		s.chMu.Lock()
		s.warm[c] = struct{}{}
		s.chMu.Unlock()
	} else {
		s.markAuthed(c)
	}
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

// authenticate runs the mutual handshake: the client asks the host to prove
// itself (host_proof_req), the host sends its chain and a signature over both
// nonces and the server ID, then the client authenticates (auth). On failure
// it queues an error and returns it; the connection is then closed.
func (c *conn) authenticate() error {
	// Pre-auth bucket: fixed, small, never disabled. The handshake reads two
	// messages (host_proof_req, auth); it bounds the exchange and a flood is
	// otherwise cut by auth_failed.
	pre := ratelimit.NewBucket(preAuthMsgRate, preAuthMsgBurst, c.srv.clock())
	nonce, err := c.sendChallenge()
	if err != nil {
		return err
	}

	_ = c.ws.SetReadDeadline(time.Now().Add(authTimeout))

	req, err := c.readHostProofReq(pre)
	if err != nil {
		return err
	}
	id, sentSeq, err := c.proveHost(nonce, req)
	if err != nil {
		return err
	}
	auth, pub, err := c.readAuth(pre, nonce, req, id)
	if err != nil {
		return err
	}
	fp, err := c.admit(auth, pub, nonce, req.Nonce, id)
	if err != nil {
		return err
	}
	if err := c.reserveSlot(auth.Warm); err != nil {
		return err
	}
	return c.register(auth, pub, fp, id, sentSeq)
}

// readPreAuth reads one handshake message, charged to the pre-auth bucket.
func (c *conn) readPreAuth(pre *ratelimit.Bucket) (proto.Envelope, error) {
	_, raw, err := c.ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	if !pre.Allow(c.srv.clock()) {
		c.sendError("rate_limited", "too many messages; slow down")
		return proto.Envelope{}, errAuthFailed
	}
	env, err := proto.Decode(raw)
	if err != nil {
		return proto.Envelope{}, errBadHandshake
	}
	return env, nil
}

func (c *conn) failAuth() error {
	c.srv.noteAuthFailure(c.ip) // blocks further connects from this IP; the reply is not delayed
	c.sendError("auth_failed", "authentication failed")
	return errAuthFailed
}

// sendChallenge queues the challenge and returns its nonce.
func (c *conn) sendChallenge() ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	chRaw, err := proto.Encode(proto.TypeChallenge, proto.Challenge{Nonce: nonce, Build: version.Version})
	if err != nil || !c.enqueue(chRaw) {
		return nil, errAuthFailed
	}
	return nonce, nil
}

// readHostProofReq reads the client's request that this host prove itself.
func (c *conn) readHostProofReq(pre *ratelimit.Bucket) (proto.HostProofReq, error) {
	var req proto.HostProofReq
	env, err := c.readPreAuth(pre)
	if errors.Is(err, errBadHandshake) {
		return req, c.failAuth()
	}
	if err != nil {
		return req, err // read error, or rate limited (already answered)
	}
	if env.Type != proto.TypeHostProofReq {
		return req, c.failAuth()
	}
	if err := json.Unmarshal(env.Data, &req); err != nil || len(req.Nonce) != nonceSize {
		return req, c.failAuth()
	}
	return req, nil
}

// proveHost proves this host to the client before it sends anything secret:
// the chain, then a signature over both nonces and the server ID. It returns
// the server ID and the head sequence of the chain it sent.
func (c *conn) proveHost(nonce []byte, req proto.HostProofReq) (chain.ID, uint64, error) {
	id := c.srv.chain.ID()
	sent := c.srv.chain.Entries()
	sentSeq := sent[len(sent)-1].Seq
	frames, err := chainFrames(sent)
	if err != nil {
		return id, 0, err
	}
	size := 0
	for _, f := range frames {
		size += len(f)
	}
	if !c.srv.chargeChain(c.ip, size) {
		c.sendError(proto.CodeServerBusy, "server busy")
		return id, 0, errAuthFailed
	}
	for _, f := range frames {
		if !c.enqueue(f) {
			return id, 0, errAuthFailed
		}
	}
	if !c.sendMsg(proto.TypeHostProof, proto.HostProof{
		PubKey: c.srv.cfg.HostKey.PublicKey(),
		Sig:    c.srv.cfg.HostKey.Sign(proto.HostProofBytes(req.Nonce, nonce, id[:])),
	}) {
		return id, 0, errAuthFailed
	}
	return id, sentSeq, nil
}

// readAuth reads the client's auth message and checks its build, shape and
// signature over both nonces and the server ID.
func (c *conn) readAuth(pre *ratelimit.Bucket, nonce []byte, req proto.HostProofReq, id chain.ID) (proto.Auth, ed25519.PublicKey, error) {
	var auth proto.Auth
	env, err := c.readPreAuth(pre)
	if errors.Is(err, errBadHandshake) {
		return auth, nil, c.failAuth()
	}
	if err != nil {
		return auth, nil, err
	}
	if env.V != proto.Version || env.Type != proto.TypeAuth {
		return auth, nil, c.failAuth()
	}
	if err := json.Unmarshal(env.Data, &auth); err != nil {
		return auth, nil, c.failAuth()
	}
	if c.refuseBuild(auth.Build) {
		return auth, nil, errAuthFailed
	}
	if !proto.ValidAuth(auth) {
		return auth, nil, c.failAuth()
	}
	pub := ed25519.PublicKey(auth.PubKey)
	if !identity.Verify(pub, proto.ClientAuthBytes(nonce, req.Nonce, id[:]), auth.Sig) {
		return auth, nil, c.failAuth()
	}
	return auth, pub, nil
}

// admit applies the ban, password, allowlist and standby checks to a client
// whose key is proven, and returns its fingerprint.
func (c *conn) admit(auth proto.Auth, pub ed25519.PublicKey, nonce, reqNonce []byte, id chain.ID) (string, error) {
	if c.srv.banned(identity.Fingerprint(pub)) {
		// The key is proven, so telling it is safe; not an auth failure.
		c.sendError(proto.CodeBanned, "you are banned from this server")
		return "", errAuthFailed
	}
	// Hosts reach each other warm and without the password: the chain already
	// vouches for their keys.
	st := c.srv.chain.State()
	hostWarm := auth.Warm && st.IsHost(pub)
	if !hostWarm && !c.srv.checkPassword(auth, nonce, reqNonce, id[:]) {
		return "", c.failAuth()
	}
	fp := identity.Fingerprint(pub)
	if !c.srv.keyAllowed(fp) {
		return "", c.failAuth()
	}

	c.srv.noteAuthSuccess(c.ip)
	if !auth.Warm && c.srv.Mode() != ModeActive {
		c.sendMsg(proto.TypeHosts, c.srv.hostsInfo())
		c.sendError(proto.CodeNotActive, "this host is on standby")
		return "", errAuthFailed
	}
	return fp, nil
}

// reserveSlot takes a warm or a live client slot.
func (c *conn) reserveSlot(warm bool) error {
	// Warm sessions are capped by their own counter at the same max_clients;
	// promotion takes a real slot.
	if warm {
		if !c.srv.reserveWarmSlot() {
			c.sendError(proto.CodeServerFull, "server is full")
			return errAuthFailed
		}
		c.warmHeld.Store(true)
	} else {
		if !c.srv.reserveClient() {
			c.sendError(proto.CodeServerFull, "server is full")
			return errAuthFailed
		}
		c.counted.Store(true)
	}
	return nil
}

// register records the authenticated identity, queues auth_ok and the
// initial frames, and joins the connection to the live set.
func (c *conn) register(auth proto.Auth, pub ed25519.PublicKey, fp string, id chain.ID, sentSeq uint64) error {
	c.warm = auth.Warm
	c.pub = pub
	c.fingerprint = fp
	c.name = names.Clean(auth.Name, discover.MaxNameRunes)
	if c.name == "" {
		c.name = "anon"
	}
	if !c.sendMandatory(proto.TypeAuthOK, proto.AuthOK{
		Fingerprint: fp,
		Roles:       c.srv.roles().RolesFor(fp),
		Channels:    c.srv.channelInfos(),
		ServerName:  c.srv.cfg.ServerName,
		ServerID:    id.String(),
	}) {
		return errAuthFailed
	}
	if !auth.Warm {
		c.sendMsg(proto.TypeHosts, c.srv.hostsInfo())
	}
	// Registered after auth_ok is queued so voice_state never precedes it.
	// Under chainMu, so no append can broadcast in between: entries added
	// since the handshake sent the chain are caught up here, and every later
	// one reaches this connection through the broadcast.
	if err := c.joinLive(sentSeq); err != nil {
		return err
	}
	// A ban appended between the admission check and markAuthed missed this
	// connection (kick only reaches authed ones).
	if c.srv.banned(fp) {
		c.sendError(proto.CodeBanned, "you are banned from this server")
		return errAuthFailed
	}
	return nil
}
