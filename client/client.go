// Package client is a headless Hermec client: it performs the
// challenge-response handshake and exposes joins, chat and a stream of
// server events. Bots, the GUI and test harnesses are built on it.
package client

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	defaultAuthTimeout = 10 * time.Second
	writeTimeout       = 10 * time.Second
	eventBuffer        = 256
	nonceSize          = 32
)

// ErrClosed is returned by operations on a closed or dead client.
var ErrClosed = errors.New("client: connection closed")

// Event is one server-originated occurrence. Exactly one field is non-nil.
// Err carries server error messages that were not the answer to a pending
// Join, plus the terminal read error if the connection died unexpectedly.
type Event struct {
	Chat     *proto.ChatMessage
	Presence *proto.Presence
	Voice    *proto.VoiceState
	Roster   *proto.Roster // full member list, re-sent on every connect/disconnect
	Chain    *chain.Entry  // a new entry, already applied to Client.Chain()
	Err      error
}

// ServerError is an error message sent by the server.
type ServerError struct {
	Code    string
	Message string
	Gen     uint32 // gen of the refused watch request, 0 if none
}

func (e *ServerError) Error() string { return e.Code + ": " + e.Message }

// Codes for refusals the server makes before the WebSocket upgrade. They come
// as an HTTP status, not a protocol error message, so Dial synthesizes them.
const (
	CodeServerBusy = "server_busy" // 503: pre-auth or per-IP cap, or a blocked IP
	CodeForbidden  = "forbidden"   // 403: the request carried an Origin header
)

// joinWaiter is a pending request answered in FIFO order: a Join (channel set)
// or a ChainAppend (sig set, resolved when that entry comes back).
type joinWaiter struct {
	channel string
	sig     []byte
	res     chan error // buffered(1)
}

// Client is an authenticated connection to a Hermec server.
type Client struct {
	ws          *websocket.Conn
	id          *identity.Identity
	chainCallMu sync.Mutex // serializes ChainAppend calls
	fingerprint string
	roles       []string
	channels    []proto.ChannelInfo
	serverName  string // from auth_ok; immutable after Dial
	serverID    string // chain.ID.String() of the verified chain; immutable after Dial
	chain       *chain.Chain

	writeMu sync.Mutex // gorilla permits a single concurrent writer

	mu      sync.Mutex // guards waiters and closed
	waiters []*joinWaiter
	closed  bool
	call    *call // active voice call, if any

	loopbackICE atomic.Bool
	audioOnly   atomic.Bool

	started     time.Time // Dial time; baseline of the first VideoStats window
	vstats      videoStats
	vsinkMu     sync.RWMutex
	vsink       VideoSink
	watchMu     sync.Mutex
	watchFP     string // sharer we asked to watch ("" = none)
	watchGen    uint64 // bumped by every Watch call
	watchAt     time.Time
	watchWire   uint32      // gen sent with the latest effective Watch; video_source must echo it
	prevWatch   *watchState // state before that Watch, restored if the server refuses it
	wireCtr     uint32      // last gen handed out; watchWire may be restored below it
	watchWait   chan error  // answer channel of the pending Watch, nil if none
	watchCallMu sync.Mutex  // serializes Watch calls
	srcHave     bool        // a video_source for watchFP arrived since the last Watch
	srcSeq      uint16      // first RTP sequence number of that source
	srcID       uint64      // bumped by every accepted video_source

	evMu     sync.RWMutex // guards closing events against emitNB
	evClosed bool

	events   chan Event
	done     chan struct{} // closed by Close; unblocks the read loop's sends
	loopDone chan struct{} // closed when the read loop has exited
	pingDone chan struct{} // closed when the pinger has exited
	rtt      *rttTracker
	once     sync.Once
}

// refusalError maps a pre-upgrade HTTP status to a typed ServerError, or nil
// for statuses that are not server refusals.
func refusalError(status int) *ServerError {
	switch status {
	case http.StatusServiceUnavailable:
		return &ServerError{Code: CodeServerBusy, Message: "server busy"}
	case http.StatusForbidden:
		return &ServerError{Code: CodeForbidden, Message: "origin refused"}
	}
	return nil
}

// Dial is DialPinned without a pin: the server ID is trusted on first use.
func Dial(ctx context.Context, url string, id *identity.Identity, name, password string) (*Client, error) {
	return DialPinned(ctx, url, id, name, password, "")
}

// DialPinned connects to url (e.g. "ws://host:port/"), makes the host prove it
// belongs to the server (a verified chain whose genesis hash equals pin, and a
// host key on the chain's host list), then authenticates as id and returns once
// auth_ok has been received. pin is a chain.ID.String(); "" trusts the first
// server ID seen, which the caller can read back with ServerID.
func DialPinned(ctx context.Context, url string, id *identity.Identity, name, password, pin string) (*Client, error) {
	if pin != "" {
		if _, err := chain.ParseID(pin); err != nil {
			return nil, fmt.Errorf("client: bad pin: %w", err)
		}
	}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) && resp != nil {
			se := refusalError(resp.StatusCode)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			if se != nil {
				return nil, fmt.Errorf("client: dial: %w", se)
			}
		}
		return nil, fmt.Errorf("client: dial: %w", err)
	}
	ws.SetReadLimit(proto.MaxMessageSize)

	// Bound the handshake by ctx: set a deadline and abort if ctx ends.
	deadline := time.Now().Add(defaultAuthTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-ctx.Done():
			ws.Close()
		case <-stop:
		}
	}()
	res, err := handshake(ws, id, name, password, pin)
	close(stop)
	<-watched
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	_ = ws.SetWriteDeadline(time.Time{})

	c := &Client{
		ws:          ws,
		id:          id,
		fingerprint: res.ok.Fingerprint,
		roles:       res.ok.Roles,
		channels:    res.ok.Channels,
		serverName:  res.ok.ServerName,
		serverID:    res.ok.ServerID,
		chain:       res.chain,
		events:      make(chan Event, eventBuffer),
		done:        make(chan struct{}),
		loopDone:    make(chan struct{}),
		pingDone:    make(chan struct{}),
		rtt:         newRTTTracker(),
		started:     time.Now(),
	}
	// Only the pong handler is set: the default ping handler keeps answering
	// the server's keepalive pings.
	ws.SetPongHandler(func(p string) error {
		c.rtt.pong(p, time.Now())
		return nil
	})
	go c.readLoop()
	c.startPinger(pingInterval, pongTimeout, time.Now)
	return c, nil
}

// handshakeResult is what a successful handshake yields.
type handshakeResult struct {
	ok    proto.AuthOK
	chain *chain.Chain
}

// handshake runs the version 2 mutual handshake: the host proves itself with
// the signed chain before the password is sent, then the client authenticates
// with a signature bound to the server ID.
func handshake(ws *websocket.Conn, id *identity.Identity, name, password, pin string) (handshakeResult, error) {
	var zero handshakeResult
	// The challenge is a v1 envelope, so a v1 host looks the same until it
	// answers host_proof_req.
	env, err := readHS(ws)
	if err != nil {
		return zero, fmt.Errorf("client: read challenge: %w", err)
	}
	if env.Type != proto.TypeChallenge {
		return zero, fmt.Errorf("client: expected challenge, got %q", env.Type)
	}
	var ch proto.Challenge
	if err := json.Unmarshal(env.Data, &ch); err != nil || len(ch.Nonce) == 0 {
		return zero, fmt.Errorf("client: bad challenge")
	}
	clientNonce := make([]byte, nonceSize)
	if _, err := rand.Read(clientNonce); err != nil {
		return zero, err
	}
	raw, err := proto.Encode(proto.TypeHostProofReq, proto.HostProofReq{Nonce: clientNonce})
	if err != nil {
		return zero, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return zero, fmt.Errorf("client: send host_proof_req: %w", err)
	}

	var entries []chain.Entry
	var total int
	for frames := 0; ; frames++ {
		env, err = readHS(ws)
		if err != nil {
			return zero, fmt.Errorf("client: read chain: %w", err)
		}
		if env.V != proto.Version {
			// A v1 host answers host_proof_req with an error (or nothing it
			// understands) instead of a chain.
			return zero, fmt.Errorf("client: %w", errOutdated())
		}
		if env.Type == proto.TypeError {
			return zero, fmt.Errorf("client: %w", handshakeError(env))
		}
		if env.Type != proto.TypeChain {
			return zero, fmt.Errorf("client: %w", badChain("expected chain, got %q", env.Type))
		}
		if frames >= maxChainFrames {
			return zero, fmt.Errorf("client: %w", badChain("too many chain frames"))
		}
		var cf proto.ChainFrame
		if err := json.Unmarshal(env.Data, &cf); err != nil {
			return zero, fmt.Errorf("client: %w", badChain("malformed chain frame"))
		}
		for _, r := range cf.Entries {
			total += len(r)
		}
		if total > maxChainBytes {
			return zero, fmt.Errorf("client: %w", badChain("chain too large"))
		}
		es, err := decodeEntries(cf.Entries)
		if err != nil {
			return zero, fmt.Errorf("client: %w", err)
		}
		entries = append(entries, es...)
		if !cf.More {
			break
		}
	}

	env, err = readHS(ws)
	if err != nil {
		return zero, fmt.Errorf("client: read host_proof: %w", err)
	}
	if env.V != proto.Version {
		return zero, fmt.Errorf("client: %w", errOutdated())
	}
	if env.Type == proto.TypeError {
		return zero, fmt.Errorf("client: %w", handshakeError(env))
	}
	var hp proto.HostProof
	if env.Type != proto.TypeHostProof || json.Unmarshal(env.Data, &hp) != nil {
		return zero, fmt.Errorf("client: %w", badChain("expected host_proof, got %q", env.Type))
	}
	verified, err := verifyHost(entries, pin, hp, clientNonce, ch.Nonce)
	if err != nil {
		return zero, fmt.Errorf("client: %w", err)
	}

	sid := verified.ID()
	raw, err = proto.Encode(proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: name, Password: password,
		Sig: id.Sign(proto.ClientAuthBytes(ch.Nonce, clientNonce, sid[:])),
	})
	if err != nil {
		return zero, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return zero, fmt.Errorf("client: send auth: %w", err)
	}
	env, err = readEnv(ws)
	if err != nil {
		return zero, fmt.Errorf("client: read auth result: %w", err)
	}
	switch env.Type {
	case proto.TypeAuthOK:
		var ok proto.AuthOK
		if err := json.Unmarshal(env.Data, &ok); err != nil {
			return zero, fmt.Errorf("client: bad auth_ok: %w", err)
		}
		if ok.ServerID != sid.String() {
			return zero, fmt.Errorf("client: %w", &ServerError{Code: proto.CodeServerIDMismatch, Message: "the host reported a different server id"})
		}
		return handshakeResult{ok: ok, chain: verified}, nil
	case proto.TypeError:
		return zero, fmt.Errorf("client: auth rejected: %w", handshakeError(env))
	default:
		return zero, fmt.Errorf("client: unexpected %q during auth", env.Type)
	}
}

func errOutdated() *ServerError {
	return &ServerError{Code: proto.CodeServerOutdated, Message: "the host runs an older HERMEC"}
}

func handshakeError(env proto.Envelope) *ServerError {
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	return &ServerError{Code: em.Code, Message: em.Message}
}

// readHS reads one handshake message, accepting both protocol versions.
func readHS(ws *websocket.Conn) (proto.Envelope, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	return proto.DecodeHandshake(raw)
}

func readEnv(ws *websocket.Conn) (proto.Envelope, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	return proto.Decode(raw)
}

// ServerID returns the server's ID (chain.ID.String()), as proven by the host
// during the handshake.
func (c *Client) ServerID() string { return c.serverID }

// Chain returns the client's live copy of the server chain.
func (c *Client) Chain() *chain.Chain { return c.chain }

// Fingerprint returns the identity fingerprint confirmed by the server.
func (c *Client) Fingerprint() string { return c.fingerprint }

// Roles returns the roles granted at auth time.
func (c *Client) Roles() []string { return append([]string(nil), c.roles...) }

// Channels returns the server's channel list from auth_ok.
func (c *Client) Channels() []proto.ChannelInfo {
	return append([]proto.ChannelInfo(nil), c.channels...)
}

// ServerName returns the display name the server announced in auth_ok, or ""
// if it has none.
func (c *Client) ServerName() string { return c.serverName }

// Events returns the event stream. It is closed when the connection dies.
// Consumers must drain it: a full buffer applies backpressure to the read
// loop (until Close).
func (c *Client) Events() <-chan Event { return c.events }

// Join requests channel and returns when the server answers with the
// channel's presence (success) or an error message. The presence is also
// delivered on Events. Error messages carry no channel, so a failure is
// attributed to the oldest pending Join.
func (c *Client) Join(ctx context.Context, channel string) error {
	w := &joinWaiter{channel: channel, res: make(chan error, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()

	if err := c.send(proto.TypeJoin, proto.Join{Channel: channel}); err != nil {
		c.removeWaiter(w)
		return err
	}
	select {
	case err := <-w.res:
		return err
	case <-ctx.Done():
		c.removeWaiter(w)
		return ctx.Err()
	}
}

// SendChat sends text to channel. It returns once the frame is written;
// server-side rejections (not_joined, forbidden) arrive as Err events.
// While a Join is pending, a rejection may instead be attributed to that Join
// (see docs/protocol.md section 5).
func (c *Client) SendChat(ctx context.Context, channel, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.send(proto.TypeChatSend, proto.ChatSend{Channel: channel, Text: text})
}

// Close tears down the connection. It is safe to call more than once and
// from any goroutine; it returns after the read loop has exited.
func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.done)
		err = c.ws.Close()
	})
	<-c.loopDone
	<-c.pingDone
	return err
}

func (c *Client) send(typ string, payload any) error {
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := c.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return fmt.Errorf("client: write: %w", err)
	}
	return nil
}

func (c *Client) removeWaiter(w *joinWaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.waiters {
		if x == w {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			return
		}
	}
}

// emit delivers ev unless the client is being closed.
func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.done:
	}
}

// emitNB delivers ev from a goroutine other than the read loop (the voice
// call). It never blocks and is dropped if the stream is closed or full.
func (c *Client) emitNB(ev Event) {
	c.evMu.RLock()
	defer c.evMu.RUnlock()
	if c.evClosed {
		return
	}
	select {
	case c.events <- ev:
	default:
	}
}

// readLoop is the only reader and the only sender on events.
func (c *Client) readLoop() {
	defer close(c.loopDone)
	defer func() {
		c.evMu.Lock()
		c.evClosed = true
		close(c.events)
		c.evMu.Unlock()
	}()
	var exitErr error
	defer func() {
		c.mu.Lock()
		c.closed = true
		ws := c.waiters
		c.waiters = nil
		cl := c.call
		c.mu.Unlock()
		for _, w := range ws {
			w.res <- ErrClosed
		}
		c.ws.Close()
		if cl != nil {
			cl.teardown()
		}
	}()

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			select {
			case <-c.done: // deliberate Close: no error event
			default:
				exitErr = fmt.Errorf("client: connection lost: %w", err)
				c.emit(Event{Err: exitErr})
			}
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			c.emit(Event{Err: err})
			continue
		}
		switch env.Type {
		case proto.TypePresence:
			var p proto.Presence
			if err := json.Unmarshal(env.Data, &p); err != nil {
				c.emit(Event{Err: fmt.Errorf("client: bad presence: %w", err)})
				continue
			}
			c.resolveJoins(p.Channel)
			c.emit(Event{Presence: &p})
		case proto.TypeChatMessage:
			var m proto.ChatMessage
			if err := json.Unmarshal(env.Data, &m); err != nil {
				c.emit(Event{Err: fmt.Errorf("client: bad chat_message: %w", err)})
				continue
			}
			c.emit(Event{Chat: &m})
		case proto.TypeVoiceState:
			var st proto.VoiceState
			if err := json.Unmarshal(env.Data, &st); err != nil {
				c.emit(Event{Err: fmt.Errorf("client: bad voice_state: %w", err)})
				continue
			}
			c.onVoiceState(st)
			c.emit(Event{Voice: &st})
		case proto.TypeRoster:
			var r proto.Roster
			if err := json.Unmarshal(env.Data, &r); err != nil {
				c.emit(Event{Err: fmt.Errorf("client: bad roster: %w", err)})
				continue
			}
			c.emit(Event{Roster: &r})
		case proto.TypeChain:
			if !c.onChain(env) {
				return
			}
		case proto.TypeRTCOffer, proto.TypeRTCAnswer, proto.TypeRTCCandidate:
			c.onRTC(env)
		case proto.TypeVideoSource:
			var vsrc proto.VideoSource
			if json.Unmarshal(env.Data, &vsrc) == nil {
				c.onVideoSource(vsrc)
			}
		case proto.TypeError:
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			se := &ServerError{Code: em.Code, Message: em.Message, Gen: em.Gen}
			if c.failOldestJoin(se) {
				continue
			}
			if !c.onError(em) {
				c.emit(Event{Err: se})
			}
		default:
			// Unknown types (e.g. channel_list, future e2ee) are ignored.
		}
	}
}

func (c *Client) resolveJoins(channel string) {
	c.mu.Lock()
	var hit []*joinWaiter
	rest := c.waiters[:0]
	for _, w := range c.waiters {
		if w.sig == nil && w.channel == channel {
			hit = append(hit, w)
		} else {
			rest = append(rest, w)
		}
	}
	c.waiters = rest
	c.mu.Unlock()
	for _, w := range hit {
		w.res <- nil
	}
}

func (c *Client) failOldestJoin(err error) bool {
	c.mu.Lock()
	if len(c.waiters) == 0 {
		c.mu.Unlock()
		return false
	}
	w := c.waiters[0]
	c.waiters = c.waiters[1:]
	c.mu.Unlock()
	w.res <- err
	return true
}
