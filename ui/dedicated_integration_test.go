package ui

import (
	"bytes"
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

// TestDedicatedValidationHeadlessIntegration exercises the UI-owned result
// ownership boundary without Ebiten or user configuration directories. Real
// WSS/ICE is covered by the client integration suite; this test uses the
// production pinned dial against a loopback-only server and injects the
// bounded media-probe result.
func TestDedicatedValidationHeadlessIntegration(t *testing.T) {
	// Even loopback UDP listeners can produce a Windows Firewall prompt. Skip
	// before server.Start binds either its TCP or media socket.
	if runtime.GOOS == "windows" {
		t.Skip("loopback dedicated validation listener may prompt Windows Firewall")
	}

	srv := server.New(server.Config{
		Addr:             "127.0.0.1:0",
		HistoryPath:      t.TempDir() + "/history.jsonl",
		Password:         "admission-secret",
		Discoverable:     false,
		ServerName:       "dedicated-test",
		Channels:         []string{"general"},
		VoiceChannels:    []string{"voice"},
		AllowLoopbackICE: true,
		Roles:            roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	if host, _, err := net.SplitHostPort(srv.Addr()); err != nil || host != "127.0.0.1" {
		t.Fatalf("test listener = %q, not loopback: %v", srv.Addr(), err)
	}
	wsURL := "ws://" + srv.Addr() + "/"
	wssURL := "wss://dedicated.test/"

	newValidationGame := func(t *testing.T) *game {
		t.Helper()
		g := addTestGame(t)
		g.add = addDedicated
		g.df.wss.setText(wssURL)
		g.df.serverID.setText(srv.ID())
		g.df.password.setText("admission-secret")
		// Keep the UI input WSS-only while dialing the disposable loopback
		// fixture through the production pinned-admission function.
		g.dedicatedDial = func(ctx context.Context, gotURL string, idArg *identity.Identity, name, password, pin string) (*client.Client, error) {
			if gotURL != wssURL {
				t.Errorf("dial URL = %q", gotURL)
			}
			if pin != srv.ID() || password != "admission-secret" {
				t.Errorf("pinned admission = pin %q password %q", pin, password)
			}
			return productionDedicatedDial(ctx, wsURL, idArg, name, password, pin)
		}
		return g
	}

	t.Run("successful pinned admission with injected probe commits once", func(t *testing.T) {
		g := newValidationGame(t)
		defer g.closeSessions()
		probed := make(chan struct{}, 1)
		g.dedicatedProbe = func(ctx context.Context, c *client.Client) error {
			probed <- struct{}{}
			return nil
		}
		passwordBuffer := g.df.password.runes
		if !g.validateDedicatedSyntax() {
			t.Fatal("validation did not start")
		}
		if got := g.df.password.String(); got != "" {
			t.Fatalf("UI password retained after start: %q", got)
		}
		for i, r := range passwordBuffer {
			if r != 0 {
				t.Fatalf("password buffer rune %d was not cleared", i)
			}
		}
		pumpDedicatedUntil(t, g, func() bool { return len(g.sess) == 1 })
		select {
		case <-probed:
		default:
			t.Fatal("injected probe was not run")
		}
		if len(g.servers) != 1 || len(g.sess) != 1 {
			t.Fatalf("committed entries=%d sessions=%d, want one each", len(g.servers), len(g.sess))
		}
		if g.servers[0].ServerID != srv.ID() || g.servers[0].PasswordKey == "" {
			t.Fatalf("saved pinned admission state = %+v", g.servers[0])
		}
		g.pumpDedicated()
		if len(g.servers) != 1 || len(g.sess) != 1 {
			t.Fatal("a completed validation committed more than once")
		}
	})

	t.Run("probe failure commits nothing and logs raw detail", func(t *testing.T) {
		g := newValidationGame(t)
		var logs bytes.Buffer
		g.setLogWriter(&logs)
		dialed := make(chan *client.Client, 1)
		g.dedicatedProbe = func(_ context.Context, c *client.Client) error {
			dialed <- c
			return errors.New("probe diagnostic for test log")
		}
		if !g.validateDedicatedSyntax() {
			t.Fatal("validation did not start")
		}
		pumpDedicatedUntil(t, g, func() bool { return g.df.err != "" })
		if g.df.err != dedicatedFriendly(errors.New("different raw detail")) {
			t.Fatalf("user error = %q", g.df.err)
		}
		if strings.Contains(g.df.err, "probe diagnostic") || !strings.Contains(logs.String(), "probe diagnostic for test log") {
			t.Fatalf("user/log error split = user %q logs %q", g.df.err, logs.String())
		}
		if len(g.servers) != 0 || len(g.sess) != 0 {
			t.Fatalf("failure committed entries=%d sessions=%d", len(g.servers), len(g.sess))
		}
		var c *client.Client
		select {
		case c = <-dialed:
		case <-time.After(5 * time.Second):
			t.Fatal("validation did not reach probe")
		}
		select {
		case _, ok := <-c.Events():
			if ok {
				t.Fatal("probe-failed client event stream remained open")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("probe-failed client was not closed")
		}
	})

	t.Run("stale completion closes client and commits nothing", func(t *testing.T) {
		g := newValidationGame(t)
		release := make(chan struct{})
		dialed := make(chan *client.Client, 1)
		g.dedicatedProbe = func(ctx context.Context, c *client.Client) error {
			dialed <- c
			<-release // deliberately ignore cancellation: completion is stale.
			return nil
		}
		if !g.validateDedicatedSyntax() {
			t.Fatal("validation did not start")
		}
		var c *client.Client
		select {
		case c = <-dialed:
		case <-time.After(5 * time.Second):
			t.Fatal("validation did not reach probe")
		}
		g.dedicatedGen++ // stale the buffered completion without cancelling it.
		close(release)
		deadline := time.Now().Add(5 * time.Second)
		for len(g.dedicatedCh) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("stale completion was not delivered")
			}
			time.Sleep(time.Millisecond)
		}
		g.pumpDedicated()
		select {
		case _, ok := <-c.Events():
			if ok {
				t.Fatal("stale client event stream remained open")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stale client was not closed")
		}
		if len(g.servers) != 0 || len(g.sess) != 0 {
			t.Fatalf("stale completion committed entries=%d sessions=%d", len(g.servers), len(g.sess))
		}
	})

	t.Run("cancellation closes client and commits nothing", func(t *testing.T) {
		g := newValidationGame(t)
		dialed := make(chan *client.Client, 1)
		g.dedicatedProbe = func(ctx context.Context, c *client.Client) error {
			dialed <- c
			<-ctx.Done()
			return ctx.Err()
		}
		if !g.validateDedicatedSyntax() {
			t.Fatal("validation did not start")
		}
		var c *client.Client
		select {
		case c = <-dialed:
		case <-time.After(5 * time.Second):
			t.Fatal("validation did not reach probe")
		}
		g.cancelDedicated()
		select {
		case _, ok := <-c.Events():
			if ok {
				t.Fatal("cancelled client event stream remained open")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled client was not closed")
		}
		if len(g.servers) != 0 || len(g.sess) != 0 {
			t.Fatalf("cancelled validation committed entries=%d sessions=%d", len(g.servers), len(g.sess))
		}
	})
}

func pumpDedicatedUntil(t *testing.T, g *game, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		g.pumpDedicated()
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for dedicated validation")
		}
		time.Sleep(time.Millisecond)
	}
	g.pumpDedicated()
}
