// Package client is a headless Hermec client: it performs the
// challenge-response handshake and exposes joins, chat and a stream of
// server events. Bots, the GUI and test harnesses are built on it.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	defaultAuthTimeout = 10 * time.Second
	writeTimeout       = 10 * time.Second
	eventBuffer        = 256
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
	Err      error
}

// ServerError is an error message sent by the server.
type ServerError struct {
	Code    string
	Message string
}

func (e *ServerError) Error() string { return e.Code + ": " + e.Message }

type joinWaiter struct {
	channel string
	res     chan error // buffered(1)
}

// Client is an authenticated connection to a Hermec server.
type Client struct {
	ws          *websocket.Conn
	fingerprint string
	roles       []string
	channels    []proto.ChannelInfo

	writeMu sync.Mutex // gorilla permits a single concurrent writer

	mu      sync.Mutex // guards waiters and closed
	waiters []*joinWaiter
	closed  bool
	call    *call // active voice call, if any

	loopbackICE atomic.Bool

	events   chan Event
	done     chan struct{} // closed by Close; unblocks the read loop's sends
	loopDone chan struct{} // closed when the read loop has exited
	once     sync.Once
}

// Dial connects to url (e.g. "ws://host:port/"), answers the server's
// challenge as id, and returns once auth_ok has been received.
func Dial(ctx context.Context, url string, id *identity.Identity, name, password string) (*Client, error) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
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
	ok, err := handshake(ws, id, name, password)
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
		fingerprint: ok.Fingerprint,
		roles:       ok.Roles,
		channels:    ok.Channels,
		events:      make(chan Event, eventBuffer),
		done:        make(chan struct{}),
		loopDone:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func handshake(ws *websocket.Conn, id *identity.Identity, name, password string) (proto.AuthOK, error) {
	env, err := readEnv(ws)
	if err != nil {
		return proto.AuthOK{}, fmt.Errorf("client: read challenge: %w", err)
	}
	if env.Type != proto.TypeChallenge {
		return proto.AuthOK{}, fmt.Errorf("client: expected challenge, got %q", env.Type)
	}
	var ch proto.Challenge
	if err := json.Unmarshal(env.Data, &ch); err != nil {
		return proto.AuthOK{}, fmt.Errorf("client: bad challenge: %w", err)
	}
	raw, err := proto.Encode(proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: name, Sig: id.Sign(ch.Nonce), Password: password,
	})
	if err != nil {
		return proto.AuthOK{}, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return proto.AuthOK{}, fmt.Errorf("client: send auth: %w", err)
	}
	env, err = readEnv(ws)
	if err != nil {
		return proto.AuthOK{}, fmt.Errorf("client: read auth result: %w", err)
	}
	switch env.Type {
	case proto.TypeAuthOK:
		var ok proto.AuthOK
		if err := json.Unmarshal(env.Data, &ok); err != nil {
			return proto.AuthOK{}, fmt.Errorf("client: bad auth_ok: %w", err)
		}
		return ok, nil
	case proto.TypeError:
		var em proto.ErrorMsg
		_ = json.Unmarshal(env.Data, &em)
		return proto.AuthOK{}, fmt.Errorf("client: auth rejected: %w", &ServerError{em.Code, em.Message})
	default:
		return proto.AuthOK{}, fmt.Errorf("client: unexpected %q during auth", env.Type)
	}
}

func readEnv(ws *websocket.Conn) (proto.Envelope, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	return proto.Decode(raw)
}

// Fingerprint returns the identity fingerprint confirmed by the server.
func (c *Client) Fingerprint() string { return c.fingerprint }

// Roles returns the roles granted at auth time.
func (c *Client) Roles() []string { return append([]string(nil), c.roles...) }

// Channels returns the server's channel list from auth_ok.
func (c *Client) Channels() []proto.ChannelInfo {
	return append([]proto.ChannelInfo(nil), c.channels...)
}

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

// readLoop is the only reader and the only sender on events.
func (c *Client) readLoop() {
	defer close(c.loopDone)
	defer close(c.events)
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
		case proto.TypeRTCOffer, proto.TypeRTCAnswer, proto.TypeRTCCandidate:
			c.onRTC(env)
		case proto.TypeError:
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			se := &ServerError{em.Code, em.Message}
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
		if w.channel == channel {
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
