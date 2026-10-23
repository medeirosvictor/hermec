package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/atotto/clipboard"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
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

// saveHostedLocal writes only the machine-local keys, the password and the
// server_id; the group fields live in the chain.
func saveHostedLocal(path string, cfg server.Config) error {
	b, err := cfg.MarshalLocalTOML()
	if err != nil {
		return err
	}
	return writeHosted(path, b)
}

func writeHosted(path string, b []byte) error {
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

// hostedServer is the server this app hosts: its machine-local config, its
// chain, the running server (nil while stopped or failed to start) and the dial
// URL.
type hostedServer struct {
	srv   *server.Host
	cfg   server.Config
	chain *chain.Chain
	id    string // chain.ID().String(), the pin the hosted session dials with
	path  string
	url   string
	err   string // why it is not running

	mu    sync.Mutex // guards known, which the host's goroutines read and grow
	known []string   // the other hosts we know, mirrored in hosted.toml
	logf  func(format string, args ...any)
}

// knownHosts is HostConfig.Known: the hosts hosted.toml remembers.
func (h *hostedServer) knownHosts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.known)
}

// saveKnown is HostConfig.SaveKnown: it adds urls to the known hosts and
// rewrites hosted.toml.
func (h *hostedServer) saveKnown(urls []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.known = state.MergeAddrs(h.known, urls, state.MaxHosts)
	cfg := h.cfg
	cfg.KnownHosts = slices.Clone(h.known)
	if err := saveHostedLocal(h.path, cfg); err != nil { // best effort: the hosts are learned again
		h.logf("save known hosts: %v", err)
	}
}

// advertiseAddrs are the ws URLs this machine can be reached at on port,
// Tailscale first.
func (g *game) advertiseAddrs(port int) []string {
	self := g.selfAddrs
	if self == nil {
		self = discover.SelfAddrs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ts, lan := self(ctx)
	var out []string
	for _, ip := range []string{ts, lan} {
		if ip != "" {
			out = append(out, fmt.Sprintf("ws://%s:%d/", ip, port))
		}
	}
	return out
}

// name is the hosted server's name: the chain's, else the config's.
func (h *hostedServer) name() string {
	if h.chain != nil {
		if st := h.chain.State(); st.HasConfig {
			return st.Config.Name
		}
	}
	return h.cfg.ServerName
}

// chainsDir is where chain files live, next to hosted.toml.
func chainsDir(hostedPath string) string {
	return filepath.Join(filepath.Dir(hostedPath), "chains")
}

// openHostedChain returns the chain for the hosted config at path. With a
// server_id in cfg it loads chains/<id>.jsonl. Without one (a pre-chain
// hosted.toml) it founds a chain from cfg's group fields, moves it to
// chains/<id>.jsonl, sets cfg.ServerID and rewrites hosted.toml with only the
// machine-local keys.
func (g *game) openHostedChain(path string, cfg *server.Config) (c *chain.Chain, err error) {
	dir := chainsDir(path)
	if cfg.ServerID != "" {
		c, err = chain.Load(filepath.Join(dir, cfg.ServerID+".jsonl"))
		if err != nil {
			return nil, err
		}
		if cfg.Password != "" { // an older hosted.toml: swap the password for its key
			id := c.ID()
			if err := migratePassword(cfg, id[:]); err != nil {
				return nil, err
			}
			if err := saveHostedLocal(path, *cfg); err != nil {
				return nil, err
			}
		}
		return c, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp(dir, ".new-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	c, _, err = server.OpenChain(filepath.Join(tmpDir, "chain.jsonl"), g.id, *cfg)
	if err != nil {
		return nil, err
	}
	id := c.ID().String()
	final := filepath.Join(dir, id+".jsonl")
	if err := c.SetPath(final); err != nil { // writes the whole chain at its final name
		return nil, err
	}
	cfg.ServerID = id
	cid := c.ID()
	if err := migratePassword(cfg, cid[:]); err != nil {
		return nil, err
	}
	if err := saveHostedLocal(path, *cfg); err != nil {
		return nil, err
	}
	return c, nil
}

// migratePassword replaces cfg.Password with its key for serverID, so
// hosted.toml holds password_key and the password is never kept or sent.
func migratePassword(cfg *server.Config, serverID []byte) error {
	if cfg.Password == "" {
		return nil
	}
	key, err := proto.PasswordKey(cfg.Password, serverID)
	if err != nil {
		return err
	}
	cfg.PasswordKey, cfg.Password = key, ""
	return nil
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
	var inert string
	if cfg.ServerID != "" { // without one, the group keys found the chain (migration)
		if set, err := server.GroupFieldsSet(path); err != nil {
			g.logf("hosted.toml: %v", err)
		} else if len(set) > 0 {
			inert = fmt.Sprintf("hosted.toml: ignoring group keys %s; the chain holds them (close the app and use hermec-server -apply-config)",
				strings.Join(set, ", "))
			g.logf("WARNING: %s", inert)
		}
	}
	c, err := g.openHostedChain(path, &cfg)
	if err != nil {
		g.notice = fmt.Sprintf("hosted chain unreadable: %v", err)
		return
	}
	if inert != "" {
		g.notice = inert
	}
	if n := c.TornTail(); n > 0 { // the more urgent notice wins
		g.logf("hosted chain ended in a torn entry; dropped %d bytes", n)
		g.notice = "hosted chain: a half-written last entry was dropped (the app closed mid-write)"
	}
	g.hosted = &hostedServer{cfg: cfg, path: path, chain: c, id: c.ID().String()}
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

// historyDir is where hosted servers keep their chat history, beside hosted.toml.
func historyDir(hostedPath string) string {
	return filepath.Join(filepath.Dir(hostedPath), "history")
}

// startHosted starts the server from g.hosted.cfg. The dial URL uses the port
// the server really got, which differs from cfg only for port 0.
func (g *game) startHosted() error {
	h := g.hosted
	if err := server.CheckHost(h.chain, g.id); err != nil {
		h.srv, h.err = nil, err.Error()
		g.logf("hosted server not started: %v", err)
		return err
	}
	serverLogf(&h.cfg)
	cfg := h.cfg
	cfg.HostKey, cfg.Chain = g.id, h.chain
	dir := historyDir(h.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.srv, h.err = nil, err.Error()
		g.logf("hosted server not started: history dir: %v", err)
		return err
	}
	cfg.HistoryPath = filepath.Join(dir, h.id+".jsonl")
	cfg.HostName = g.defaultName()
	h.logf = g.logf
	h.known = slices.Clone(h.cfg.KnownHosts)
	srv := server.NewHost(server.HostConfig{
		Server: cfg, Known: h.knownHosts, SaveKnown: h.saveKnown,
		Notify: func(msg string) { g.logf("%s", msg) },
	})
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
	_, port, err := net.SplitHostPort(srv.Server().Addr())
	if err != nil {
		_ = srv.Shutdown(context.Background())
		h.srv, h.err = nil, err.Error()
		return err
	}
	if p := hostedPort(h.cfg); p != 0 { // port 0 has no address to advertise
		go func() { srv.Server().SetAdvertise(g.advertiseAddrs(p)) }() // the lookup can be slow
	}
	h.srv, h.err = srv, ""
	h.url = "ws://" + net.JoinHostPort("127.0.0.1", port) + "/"
	g.logf("hosting %q on %s", h.name(), h.url)
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
		g.notice = g.hosted.err
	}
}

// hostedStopped reports whether key is the hosted tile while its server is not
// running; its way back is Start hosting, not Retry.
func (g *game) hostedStopped(key string) bool {
	return key == hostedKey && g.hosted != nil && g.hosted.srv == nil
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
	g.dropWarm(s)
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
	if g.hostedTweak != nil {
		g.hostedTweak(&cfg)
	}
	cfg.ServerID = "" // openHostedChain founds the chain and saves hosted.toml
	c, err := g.openHostedChain(path, &cfg)
	if err != nil {
		return err
	}
	g.hostedPath = path
	g.hosted = &hostedServer{cfg: cfg, path: path, chain: c, id: c.ID().String()}
	if err := g.startHosted(); err != nil {
		msg := g.hosted.err
		g.hosted = nil
		_ = os.Remove(path)
		_ = os.Remove(filepath.Join(chainsDir(path), c.ID().String()+".jsonl"))
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

// removeFile is a seam for tests.
var removeFile = os.Remove

// leaveHosted stops hosting, drops the session and deletes the saved config.
func (g *game) leaveHosted() {
	h := g.hosted
	if h == nil {
		return
	}
	g.stopHosted()
	g.disconnect(hostedKey)
	if err := removeFile(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		g.notice = fmt.Sprintf("couldn't delete hosted.toml: %v", err)
	}
	g.hosted = nil
}

// inviteResult is the outcome of a background address lookup.
type inviteResult struct {
	ts, lan string
	port    int
}

// copyInvite looks up this machine's addresses in the background; pumpInvite
// copies the result. A click while a lookup is in flight does nothing.
func (g *game) copyInvite() {
	h := g.hosted
	if h == nil || h.srv == nil || g.inviteBusy {
		return
	}
	if g.inviteCh == nil {
		g.inviteCh = make(chan inviteResult, 1)
	}
	self := g.selfAddrs
	if self == nil {
		self = discover.SelfAddrs
	}
	_, p, _ := net.SplitHostPort(h.srv.Server().Addr())
	port, _ := strconv.Atoi(p)
	g.inviteBusy = true
	ch := g.inviteCh
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ts, lan := self(ctx)
		ch <- inviteResult{ts: ts, lan: lan, port: port}
	}()
}

// serverIDFor is the known server ID of the tile at key: the live client's,
// else (for the hosted tile) the hosted chain's, else the pin saved with the
// entry. Empty when unknown.
func (g *game) serverIDFor(key string) string {
	if s := g.sess[key]; s != nil && s.c != nil {
		if id := s.c.ServerID(); id != "" {
			return id
		}
	}
	if key == hostedKey && g.hosted != nil {
		return g.hosted.id
	}
	if e, ok := g.entryFor(key); ok && e.ServerID != "" {
		return e.ServerID
	}
	return ""
}

// shortID is the "srv-..." form of a full server ID, or id when unparseable.
func shortID(id string) string {
	if p, err := chain.ParseID(id); err == nil {
		return p.Short()
	}
	return id
}

// copyID puts a full server ID on the clipboard and reports it on the viewed
// session's status line.
func (g *game) copyID(id string) {
	s := g.cur()
	if hs := g.sess[hostedKey]; hs != nil && g.hosted != nil && g.hosted.id == id {
		s = hs
	}
	if err := clipboardWrite(id); err != nil {
		s.st.Status = "copy failed: " + err.Error()
	} else {
		s.st.Status = "copied " + id
	}
}

// pumpInvite applies a finished lookup: the address goes to the clipboard and
// the outcome to the viewed session's status line.
func (g *game) pumpInvite() {
	select {
	case r := <-g.inviteCh:
		g.inviteBusy = false
		addr := inviteAddress(r.ts, r.lan, r.port)
		s := g.cur()
		if h := g.sess[hostedKey]; h != nil {
			s = h
		}
		st := &s.st.Status
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
	default:
	}
}
