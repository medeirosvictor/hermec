package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/atotto/clipboard"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// wsaEADDRINUSE is WSAEADDRINUSE; on Windows it is not syscall.EADDRINUSE.
const wsaEADDRINUSE = syscall.Errno(10048)

// hostedConfig builds the config for a server hosted from inside the app.
func hostedConfig(name, password string, port int, adminFP string) server.Config {
	cfg := server.DefaultConfig()
	cfg.Addr = fmt.Sprintf(":%d", port)
	cfg.ServerName = state.CleanName(name, state.MaxServerNameRunes)
	cfg.Password = password
	cfg.VoiceChannels = []string{"voice"}
	cfg.Roles.Grants = map[string][]string{adminFP: {"admin"}}
	return cfg
}

// saveHosted writes cfg atomically; the file holds the password, so 0600.
func saveHosted(path string, cfg server.Config) error {
	b, err := cfg.MarshalTOML()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hosted-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// loadHosted reads the hosted config; a missing file is ok=false, err=nil.
func loadHosted(path string) (cfg server.Config, ok bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return server.Config{}, false, nil
	}
	cfg, err = server.LoadConfig(path)
	if err != nil {
		return server.Config{}, false, err
	}
	return cfg, true, nil
}

// hostedPort parses the port from cfg.Addr, or 0 when it has none.
func hostedPort(cfg server.Config) int {
	_, p, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}

// isPortInUse reports whether err means the listen port is already taken.
func isPortInUse(err error) bool {
	if errors.Is(err, server.ErrDiscoveryBind) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == wsaEADDRINUSE || errno == syscall.EADDRINUSE
	}
	return false
}

func portInUseMsg(port int) string {
	return fmt.Sprintf("port %d is in use (is hermec-server running?)", port)
}

// inviteAddress is the address to give friends, preferring Tailscale.
func inviteAddress(tsIP, lanIP string, port int) string {
	switch {
	case tsIP != "":
		return fmt.Sprintf("ws://%s:%d/", tsIP, port)
	case lanIP != "":
		return fmt.Sprintf("ws://%s:%d/", lanIP, port)
	}
	return noInviteAddr
}

// noInviteAddr is inviteAddress's answer when no usable interface exists.
const noInviteAddr = "no network address found"

// hostedKey identifies the server hosted from inside the app in servers.toml;
// its real URL carries the port the server actually got.
const hostedKey = "hermec://hosted"

// stoppedMsg is the failed tile's text while the hosted server is stopped.
const stoppedMsg = "not hosting — click ↻ to start"

// clipboardWrite is a seam for tests.
var clipboardWrite = clipboard.WriteAll

// hostedServer is the server this app hosts: its saved config, the running
// server (nil while stopped or failed to start) and the dial URL.
type hostedServer struct {
	srv  *server.Server
	cfg  server.Config
	path string
	url  string
	err  string // why it is not running
}

// hostedFile is where the hosted config lives for this run.
func (g *game) hostedFile() string {
	if g.hostedPath != "" {
		return g.hostedPath
	}
	if g.opts.HostedPath != "" {
		return g.opts.HostedPath
	}
	p, _ := DefaultHostedPath()
	return p
}

// bootHosted loads the hosted config at path (empty: the default) and, if
// there is one, starts the server and opens its session. A failure to start
// leaves a failed tile instead of an error. Call it after loadServers.
func (g *game) bootHosted(path string) {
	if path == "" {
		path = g.hostedFile()
	}
	g.hostedPath = path
	cfg, ok, err := loadHosted(path)
	if err != nil {
		g.notice = fmt.Sprintf("hosted.toml unreadable: %v", err)
		return
	}
	if !ok {
		return
	}
	g.hosted = &hostedServer{cfg: cfg, path: path}
	_ = g.startHosted() // the failure is in g.hosted.err and shows on the tile
	g.openSession(hostedKey, g.defaultName())
}

// defaultName is the display name for a session opened without a form.
func (g *game) defaultName() string {
	if n := g.connect.name.String(); n != "" {
		return n
	}
	return g.opts.Name
}

// startHosted starts the server from g.hosted.cfg. The dial URL uses the port
// the server really got, which differs from cfg only for port 0.
func (g *game) startHosted() error {
	h := g.hosted
	srv := server.New(h.cfg)
	if err := srv.Start(); err != nil {
		h.srv = nil
		if port := hostedPort(h.cfg); isPortInUse(err) {
			h.err = portInUseMsg(port)
		} else {
			h.err = err.Error()
		}
		g.logf("hosted server failed: %v", err)
		return err
	}
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		_ = srv.Shutdown(context.Background())
		h.srv, h.err = nil, err.Error()
		return err
	}
	h.srv, h.err = srv, ""
	h.url = "ws://" + net.JoinHostPort("127.0.0.1", port) + "/"
	g.logf("hosting %q on %s", h.cfg.ServerName, h.url)
	return nil
}

// startHostedNow starts a stopped hosted server: through retryNow when its
// tile has a session, directly otherwise.
func (g *game) startHostedNow() {
	if g.hosted == nil || g.hosted.srv != nil {
		return
	}
	if g.sess[hostedKey] != nil {
		g.retryNow(hostedKey)
	} else if g.startHosted() != nil {
		g.cur().st.Status = g.hosted.err
	}
}

// stopHosted shuts the hosted server down and marks its session failed.
func (g *game) stopHosted() {
	h := g.hosted
	if h == nil || h.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.srv.Shutdown(ctx); err != nil {
		g.logf("hosted server shutdown: %v", err)
	}
	h.srv, h.err = nil, stoppedMsg
	s := g.sess[hostedKey]
	if s == nil {
		return
	}
	g.endVoiceFor(hostedKey)
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
		s.st.Apply(client.Event{}, false)
	}
	s.dialing = false // a dial in flight is now stale and its client gets closed
	s.retryAt = time.Time{}
	s.failed, s.lastErr = true, stoppedMsg
	s.st.ConnectErr = stoppedMsg
}

// createHosted saves a new hosted config, starts it and opens its session. If
// the server cannot start nothing is kept, so the caller can show the error.
func (g *game) createHosted(name, password string, port int) error {
	if g.hosted != nil {
		return errors.New("already hosting a server")
	}
	path := g.hostedFile()
	cfg := hostedConfig(name, password, port, g.fp)
	if err := saveHosted(path, cfg); err != nil {
		return err
	}
	g.hostedPath = path
	g.hosted = &hostedServer{cfg: cfg, path: path}
	if err := g.startHosted(); err != nil {
		msg := g.hosted.err
		g.hosted = nil
		_ = os.Remove(path)
		return errors.New(msg)
	}
	if s := g.openSession(hostedKey, g.defaultName()); s != nil {
		g.view(s.key)
	}
	return nil
}

// create is what the Add a Server page calls; createFn lets tests swap it.
func (g *game) create(name, pass string, port int) error {
	if g.createFn != nil {
		return g.createFn(name, pass, port)
	}
	return g.createHosted(name, pass, port)
}

// leaveHosted stops hosting, drops the session and deletes the saved config.
func (g *game) leaveHosted() {
	h := g.hosted
	if h == nil {
		return
	}
	g.stopHosted()
	g.disconnect(hostedKey)
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		g.notice = fmt.Sprintf("delete hosted.toml: %v", err)
	}
	g.hosted = nil
}

// copyInvite puts the address friends should use on the clipboard and reports
// the outcome on the viewed session's status line.
func (g *game) copyInvite() {
	h := g.hosted
	if h == nil || h.srv == nil {
		return
	}
	self := g.selfAddrs
	if self == nil {
		self = discover.SelfAddrs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ts, lan := self(ctx)
	_, p, _ := net.SplitHostPort(h.srv.Addr())
	port, _ := strconv.Atoi(p)
	addr := inviteAddress(ts, lan, port)
	st := &g.cur().st.Status
	switch {
	case addr == noInviteAddr:
		*st = addr
	default:
		if err := clipboardWrite(addr); err != nil {
			*st = "copy failed: " + err.Error()
		} else {
			*st = "copied " + addr
		}
	}
}
