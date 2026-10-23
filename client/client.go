// Package client is a headless Hermec client: it performs the
// challenge-response handshake and exposes joins, chat and a stream of
// server events. Bots, the GUI and test harnesses are built on it.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	writeTimeout = 10 * time.Second
	eventBuffer  = 256
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
	Roster   *proto.Roster  // full member list, re-sent on every connect/disconnect
	Chain    *chain.Entry   // a new entry, already applied to Client.Chain()
	History  *proto.History // a page of stored chat history
	Hosts    *proto.Hosts   // the active host and the standby
	Move     *proto.Move    // the host asks us to reconnect elsewhere
	// ActiveLost is true once, when the session's host goes silent or drops
	// after its heartbeats began (on a warm session that host is the standby).
	// It is the cue to ask the standby to take over.
	ActiveLost bool
	Err        error
}

// ServerError is an error message sent by the server.
type ServerError = hshake.ServerError

// Codes for refusals the server makes before the WebSocket upgrade. They come
// as an HTTP status, not a protocol error message, so Dial synthesizes them.
const (
	CodeServerBusy = proto.CodeServerBusy // 503: pre-auth or per-IP cap, or a blocked IP
	CodeForbidden  = "forbidden"          // 403: the request carried an Origin header
)

// joinWaiter is a pending request answered in FIFO order: a Join (channel set)
// or a ChainAppend (sig set, resolved when that entry comes back).
type joinWaiter struct {
	channel string
	sig     []byte
	ref     string     // chain_append request ref; errors for it echo this
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
	pwKey       []byte // the password key this login used; nil without a password
	chain       *chain.Chain

	writeMu sync.Mutex // gorilla permits a single concurrent writer

	mu      sync.Mutex // guards waiters and closed
	waiters []*joinWaiter
	closed  bool
	call    *call // active voice call, if any

	warm      atomic.Bool  // paused session until a takeover promotes it
	lastHB    atomic.Int64 // clock time of the newest hb in unix nanoseconds; 0 before the first
	lostFired atomic.Bool  // ActiveLost was emitted

	loopbackICE atomic.Bool
	audioOnly   atomic.Bool

	wpMu sync.Mutex // guards wp
	wp   *warmPeer  // data-channel PeerConnection to the standby, if opened

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
	return DialWith(ctx, url, id, name, password, DialOpts{Pin: pin})
}

// DialOpts are the optional parts of DialWith and Handshake.
type DialOpts = hshake.Opts

// HandshakeResult is what a successful Handshake yields: the verified chain,
// its server ID, the auth_ok and the password key used.
type HandshakeResult = hshake.Result

// Handshake runs the mutual handshake over ws, an open connection to a host:
// the host proves itself with its signed chain, then id authenticates (with
// the password proof when there is a key or password). ctx bounds it and
// closes ws if it ends first. Bots and the GUI use Dial; hosts linking to each
// other call this directly with Warm set. ws is not closed on error.
func Handshake(ctx context.Context, ws *websocket.Conn, id *identity.Identity, name, password string, o DialOpts) (*HandshakeResult, error) {
	return hshake.Run(ctx, ws, id, name, password, o)
}

// DialWith is DialPinned with options. The password never goes over the wire:
// the client proves it knows it with an HMAC under the derived key.
func DialWith(ctx context.Context, url string, id *identity.Identity, name, password string, o DialOpts) (*Client, error) {
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
	res, err := Handshake(ctx, ws, id, name, password, o)
	if err != nil {
		ws.Close()
		return nil, err
	}

	c := &Client{
		ws:          ws,
		id:          id,
		fingerprint: res.OK.Fingerprint,
		roles:       res.OK.Roles,
		channels:    res.OK.Channels,
		serverName:  res.OK.ServerName,
		serverID:    res.OK.ServerID,
		pwKey:       res.Key,
		chain:       res.Chain,
		events:      make(chan Event, eventBuffer),
		done:        make(chan struct{}),
		loopDone:    make(chan struct{}),
		pingDone:    make(chan struct{}),
		rtt:         newRTTTracker(),
		started:     time.Now(),
	}
	c.warm.Store(o.Warm)
	// Only the pong handler is set: the default ping handler keeps answering
	// the server's keepalive pings.
	ws.SetPongHandler(func(p string) error {
		c.rtt.pong(p, time.Now())
		return nil
	})
	go c.readLoop()
	go c.watchdog(time.Now)
	c.startPinger(pingInterval, pongTimeout, time.Now)
	return c, nil
}

// ServerID returns the server's ID (chain.ID.String()), as proven by the host
// during the handshake.
func (c *Client) ServerID() string { return c.serverID }

// PasswordKey returns the key derived from the password for this server, which
// a caller can cache and pass back in DialOpts.PasswordKey so the next login
// skips the derivation. nil when the login used no password.
func (c *Client) PasswordKey() []byte { return c.pwKey }

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

// HistoryBefore asks for the page of messages older than beforeID in channel.
// beforeTS is that message's timestamp; the host uses it when it no longer
// holds beforeID (zero omits it). The answer arrives as an Event with History
// set and Before == beforeID.
func (c *Client) HistoryBefore(channel, beforeID string, beforeTS time.Time) error {
	return c.send(proto.TypeHistoryBefore, proto.HistoryBefore{Channel: channel, BeforeID: beforeID, BeforeTS: beforeTS})
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
		c.closeWarmPeer()
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
				c.connLost()
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
		case proto.TypeHistory:
			var h proto.History
			if err := json.Unmarshal(env.Data, &h); err != nil {
				c.emit(Event{Err: fmt.Errorf("client: bad history: %w", err)})
				continue
			}
			c.emit(Event{History: &h})
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
			c.promoted()
			c.emit(Event{Roster: &r})
		case proto.TypeHosts:
			c.onHosts(env)
		case proto.TypeMove:
			c.onMove(env)
		case proto.TypeHeartbeat:
			c.lastHB.Store(time.Now().UnixNano())
		case proto.TypeChain:
			if !c.onChain(env) {
				return
			}
		case proto.TypeRTCOffer, proto.TypeRTCAnswer, proto.TypeRTCCandidate:
			c.onRTC(env)
		case proto.TypeWarmAnswer:
			c.onWarmAnswer(env)
		case proto.TypeVideoSource:
			var vsrc proto.VideoSource
			if json.Unmarshal(env.Data, &vsrc) == nil {
				c.onVideoSource(vsrc)
			}
		case proto.TypeError:
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			se := &ServerError{Code: em.Code, Message: em.Message, Gen: em.Gen}
			if (em.Ref == "" && c.failTakeover(se)) || c.failWaiter(em.Ref, se) {
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

// failWaiter fails the chain_append waiter ref names, or, for an error with
// no ref, the oldest pending Join. A pending Takeover is answered only by
// failTakeover.
func (c *Client) failWaiter(ref string, err error) bool {
	c.mu.Lock()
	for i, w := range c.waiters {
		if w.ref == ref && w.channel != takeoverKey {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			c.mu.Unlock()
			w.res <- err
			return true
		}
	}
	c.mu.Unlock()
	return false
}
