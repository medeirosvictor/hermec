package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
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

	// Set once authenticated; only touched by readLoop.
	fingerprint string
	name        string
}

func newConn(s *Server, ws *websocket.Conn) *conn {
	return &conn{srv: s, ws: ws, send: make(chan []byte, sendBuffer)}
}

// enqueue queues an encoded message for the writer. It reports false if the
// connection is closed or its outbound buffer is full (slow consumer).
func (c *conn) enqueue(raw []byte) bool {
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

// finish stops accepting outbound messages; the writer drains what is already
// queued (e.g. a final error), then closes the socket.
func (c *conn) finish() {
	c.srv.unregister(c)
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

	if err := c.authenticate(); err != nil {
		return
	}
	_ = c.ws.SetReadDeadline(time.Time{})

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			c.sendError("bad_request", "malformed message")
			continue
		}
		c.route(env)
	}
}

// route handles one post-auth message. The next task replaces this stub with
// channel join/leave/chat handling.
func (c *conn) route(env proto.Envelope) {
	c.sendError("bad_request", "unsupported message type "+env.Type)
}

var errAuthFailed = errors.New("auth failed")

// authenticate runs the one-shot challenge-response handshake. On failure it
// queues auth_failed and returns an error; the connection is then closed.
func (c *conn) authenticate() error {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if !c.sendMsg(proto.TypeChallenge, proto.Challenge{Nonce: nonce}) {
		return errAuthFailed
	}

	_ = c.ws.SetReadDeadline(time.Now().Add(authTimeout))
	_, raw, err := c.ws.ReadMessage()
	if err != nil {
		return err
	}

	fail := func() error {
		c.sendError("auth_failed", "authentication failed")
		return errAuthFailed
	}

	env, err := proto.Decode(raw)
	if err != nil || env.Type != proto.TypeAuth {
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
	if !identity.Verify(pub, nonce, auth.Sig) {
		return fail()
	}
	if pw := c.srv.cfg.Password; pw != "" &&
		subtle.ConstantTimeCompare([]byte(pw), []byte(auth.Password)) != 1 {
		return fail()
	}
	fp := identity.Fingerprint(pub)
	if c.srv.allowed != nil {
		if _, ok := c.srv.allowed[fp]; !ok {
			return fail()
		}
	}

	c.fingerprint = fp
	c.name = auth.Name
	c.sendMsg(proto.TypeAuthOK, proto.AuthOK{
		Fingerprint: fp,
		Roles:       c.srv.cfg.Roles.RolesFor(fp),
		Channels:    c.srv.cfg.Channels,
	})
	return nil
}
