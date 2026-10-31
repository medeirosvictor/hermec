package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/ui/state"
)

// maxDedicatedPasswordBytes matches the documented command-line secret bound.
// The UI owns this short-lived buffer and clears it whenever a draft ends.
const maxDedicatedPasswordBytes = 256

type dedicatedForm struct {
	wss, serverID, password field
	focus                   int
	err, status             string
	validated               bool
}

func (f *dedicatedForm) clearPassword() {
	for i := range f.password.runes {
		f.password.runes[i] = 0
	}
	f.password.runes = nil
	f.password.back = 0
}

func (f *dedicatedForm) reset() { f.clearPassword(); *f = dedicatedForm{} }

func (f *dedicatedForm) edit(k editKeys, rs []rune) {
	switch f.focus {
	case 0:
		f.wss.edit(k, rs, maxFieldRunes, nil, false)
	case 1:
		f.serverID.edit(k, rs, 52, nil, false)
	case 2:
		f.password.edit(k, rs, maxDedicatedPasswordBytes, nil, false)
		// The documented limit is bytes, not runes. Trim whole runes only.
		for len([]byte(f.password.String())) > maxDedicatedPasswordBytes {
			f.password.runes = f.password.runes[:len(f.password.runes)-1]
		}
	}
}

func (f *dedicatedForm) details() (state.DedicatedDetails, error) {
	return state.ValidateDedicatedDetails(state.DedicatedDetails{WSSURL: strings.TrimSpace(f.wss.String()), ServerID: strings.TrimSpace(f.serverID.String())})
}

// importDedicatedDetails is deliberately called only by the explicit Import
// control. Normal focus/paste never reads the clipboard as a credential.
func (g *game) importDedicatedDetails() {
	// Replacing either endpoint component makes every outstanding probe stale.
	// A password belongs to the old endpoint, never to imported details.
	g.invalidateDedicatedValidation()
	g.df.clearPassword()
	s, err := clipboardRead()
	if err != nil {
		g.df.err = "could not read connection details"
		return
	}
	d, err := state.UnmarshalDedicatedDetails([]byte(s))
	if err != nil {
		g.df.err = "invalid connection details (sender must use the same build)"
		return
	}
	g.df.wss.setText(d.WSSURL)
	g.df.serverID.setText(d.ServerID)
	g.df.err, g.df.status, g.df.validated = "", "connection details imported", false
}

func (g *game) copyDedicatedDetails(d state.DedicatedDetails) {
	b, err := state.MarshalDedicatedDetails(d)
	if err == nil {
		err = clipboardWrite(string(b))
	}
	if err != nil {
		g.cur().st.Status = "copy failed"
	} else {
		g.cur().st.Status = "connection details copied"
	}
}

// dedicatedDetailsFor is also the menu eligibility check. A regular LAN entry
// cannot become exportable merely because it happens to have a server ID.
func (g *game) dedicatedDetailsFor(key string) (state.DedicatedDetails, bool) {
	for _, e := range g.servers {
		if e.URL != key {
			continue
		}
		d, err := state.ValidateDedicatedDetails(state.DedicatedDetails{WSSURL: e.URL, ServerID: e.ServerID})
		return d, err == nil
	}
	return state.DedicatedDetails{}, false
}

// dedicatedResult is owned by its worker until pumpDedicated accepts its
// generation. Rejected results always close c, preventing stale workers from
// creating a session or leaking a websocket/ICE transport.
type dedicatedResult struct {
	gen uint64
	d   state.DedicatedDetails
	c   *client.Client
	err error
	// ack transfers client ownership. false means the worker must close it;
	// this lets cancellation clean up even when the UI stops ticking.
	ack chan bool
}

type dedicatedProbeSource struct{}

func (dedicatedProbeSource) ReadOpusFrame() ([]byte, error) {
	time.Sleep(20 * time.Millisecond)
	return []byte{0xf8, 0xff, 0xfe}, nil
}

type dedicatedProbeSink struct{}

func (dedicatedProbeSink) WriteOpusFrame(string, []byte) {}

// dedicatedDial and dedicatedMediaProbe are deliberately narrow seams around
// the production operations. They let the UI lifecycle test use a loopback
// client and a bounded probe outcome; client integration tests cover real
// WSS/ICE transport separately.
type dedicatedDial func(context.Context, string, *identity.Identity, string, string, string) (*client.Client, error)
type dedicatedMediaProbe func(context.Context, *client.Client) error

func productionDedicatedDial(ctx context.Context, url string, id *identity.Identity, name, password, pin string) (*client.Client, error) {
	return client.DialPinned(ctx, url, id, name, password, pin)
}

func productionDedicatedProbe(ctx context.Context, c *client.Client) error {
	voice := ""
	for _, ch := range c.Channels() {
		if ch.Type == "voice" {
			voice = ch.Name
			break
		}
	}
	if voice == "" {
		return fmt.Errorf("no voice channel")
	}
	vctx, stop := context.WithTimeout(ctx, 20*time.Second)
	err := c.JoinVoice(vctx, voice, dedicatedProbeSource{}, dedicatedProbeSink{})
	stop()
	lctx, leave := context.WithTimeout(context.Background(), 2*time.Second)
	_ = c.LeaveVoice(lctx)
	leave()
	return err
}

// runDedicatedValidation is the production-shaped pinned admission followed
// by the disposable media reachability probe. The individual operations are
// injectable only to bound the UI-owned headless lifecycle test.
func (g *game) runDedicatedValidation(ctx context.Context, d state.DedicatedDetails, name, password string) (*client.Client, error) {
	dial := g.dedicatedDial
	if dial == nil {
		dial = productionDedicatedDial
	}
	probe := g.dedicatedProbe
	if probe == nil {
		probe = productionDedicatedProbe
	}
	c, err := dial(ctx, d.WSSURL, g.id, name, password, d.ServerID)
	if err != nil {
		return nil, err
	}
	if err := probe(ctx, c); err != nil {
		return c, err
	}
	return c, nil
}

// validateDedicatedSyntax starts the dedicated-only pinned validation. Unlike
// openSession it creates neither a pending tile nor a saved entry. The draft
// password is copied only into the worker invocation, then the UI buffer is
// cleared immediately; it is never logged or exported.
func (g *game) validateDedicatedSyntax() bool {
	// Even a syntactically invalid retry replaces the previous draft and must
	// prevent that previous result from being accepted.
	g.invalidateDedicatedValidation()
	d, err := g.df.details()
	if err != nil {
		g.df.err = err.Error()
		g.df.clearPassword()
		return false
	}
	for _, e := range g.servers {
		if e.URL == d.WSSURL && e.ServerID != "" && e.ServerID != d.ServerID {
			g.df.err = "saved server ID does not match connection details"
			g.df.clearPassword()
			return false
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.dedicatedStop = cancel
	g.dedicatedGen++
	gen, name, pass := g.dedicatedGen, g.connect.name.String(), g.df.password.String()
	if name == "" {
		name = g.opts.Name
	}
	g.df.clearPassword()
	g.df.err = ""
	g.df.status = "validating secure connection…"
	g.df.validated = false
	if g.dedicatedCh == nil {
		g.dedicatedCh = make(chan dedicatedResult, 1)
	}
	go func() {
		c, err := g.runDedicatedValidation(ctx, d, name, pass)
		r := dedicatedResult{gen: gen, d: d, c: c, err: err, ack: make(chan bool, 1)}
		// Do not strand a client in the result buffer on shutdown. Once sent,
		// ownership remains with this worker until pumpDedicated acknowledges it.
		select {
		case <-ctx.Done():
			if c != nil {
				_ = c.Close()
			}
			return
		default:
		}
		select {
		case g.dedicatedCh <- r:
		case <-ctx.Done():
			if c != nil {
				_ = c.Close()
			}
			return
		}
		select {
		case accepted := <-r.ack:
			if !accepted && c != nil {
				_ = c.Close()
			}
		case <-ctx.Done():
			// An acknowledgement wins if pumpDedicated received the result at
			// the same instant cancellation happened.
			select {
			case accepted := <-r.ack:
				if !accepted && c != nil {
					_ = c.Close()
				}
			default:
				if c != nil {
					_ = c.Close()
				}
			}
		}
	}()
	return true
}

func dedicatedFriendly(err error) string {
	if err == nil {
		return "client-to-server media transport reachability (UDP/ICE) verified; this is not remote-media proof"
	}
	if strings.Contains(err.Error(), "no voice channel") {
		return "No voice channel is configured; ask the operator to configure one."
	}
	if errors := strings.ToLower(err.Error()); strings.Contains(errors, "tls") || strings.Contains(errors, "certificate") {
		return "Secure connection could not be verified."
	}
	return "Could not validate this dedicated server. Check the URL, server ID, password, and operator setup."
}

func (g *game) pumpDedicated() {
	if g.dedicatedCh == nil {
		return
	}
	select {
	case r := <-g.dedicatedCh:
		if r.gen != g.dedicatedGen || g.dedicatedStop == nil || g.add != addDedicated {
			r.ack <- false
			return
		}
		g.dedicatedStop = nil
		if r.err != nil {
			r.ack <- false
			g.df.err = dedicatedFriendly(r.err)
			// The UI stays deliberately coarse; the session log retains the raw
			// operation error for support. Validation never formats its inputs.
			g.logf("dedicated validation failed: %v", r.err)
			return
		}
		// openSession never replaces an existing session. Preserve that lifecycle
		// rule here too: this probe's client is disposable, even after a successful
		// validation, when the URL is already live or dialing.
		if s := g.sess[r.d.WSSURL]; s != nil {
			r.ack <- false
			g.view(s.key)
			g.df.status = "Already connected to this dedicated server."
			g.df.validated = true
			return
		}
		r.ack <- true
		// The retained client was pin-validated and cleanly left voice. Later
		// reconnects use the saved pin and derived proof key, never the password.
		s := newSession(r.d.WSSURL, r.d.WSSURL, state.HostPort(r.d.WSSURL))
		s.c = r.c
		s.everUp = true
		s.c.SetVideoSink(&g.vsink)
		s.c.SetMediaSink(&g.mframes)
		s.st.SetConnected(s.c.Channels(), s.c.Fingerprint())
		g.sess[s.key] = s
		g.onConnected(s)
		// onConnected creates the entry; only now can its verified chain head be
		// recorded and persisted. There was no entry before complete validation.
		if g.noteChainSeq(s) {
			ch := ""
			if len(s.st.Channels) != 0 {
				ch = s.st.Channels[s.st.Active]
			}
			g.saveServer(s, ch)
		}
		g.view(s.key)
		g.df.status = dedicatedFriendly(nil)
		g.df.validated = true
	default:
	}
}

func (g *game) invalidateDedicatedValidation() {
	if g.dedicatedStop != nil {
		g.dedicatedStop()
		g.dedicatedStop = nil
	}
	g.dedicatedGen++
}

func (g *game) cancelDedicated() {
	g.invalidateDedicatedValidation()
	g.df.clearPassword()
}

func dedicatedMasked(f *field) string { return strings.Repeat("•", len(f.runes)) }

// dedicatedCaretPrefix deliberately measures the displayed bullets, not the
// secret rune prefix. Besides avoiding disclosure through glyph widths, this
// keeps the caret aligned for non-ASCII passwords.
func dedicatedCaretPrefix(label string, f *field) string {
	return label + strings.Repeat("•", f.caret())
}
