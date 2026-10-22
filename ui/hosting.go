package ui

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// A friend the founder granted hosting can let this PC host the server: a
// server.Host runs beside the app, follows the active host as standby and
// takes over when it disappears. Its files live next to hosted.toml.

// Friend hosts listen on the first free port of this range.
const (
	friendPortMin = 7697
	friendPortMax = 7707
)

// friendStopWait is how long shutting down waits for the friend hosts to stop.
const friendStopWait = 3 * time.Second

// friendReachNotice is shown once, when this PC first starts hosting for a friend.
const friendReachNotice = "friends must be able to reach this PC (share it on Tailscale)"

// hostingFile is <config>/hermec/hosting/<server-id>.toml. PasswordKey is the
// group's key as the session logged in with it, so a host that starts with no
// other host to follow still requires the group password.
type hostingFile struct {
	Port        int      `toml:"port"`
	PasswordKey string   `toml:"password_key,omitempty"` // hex
	KnownHosts  []string `toml:"known_hosts,omitempty"`
	Enabled     bool     `toml:"enabled"`
}

// friendHost is a server.Host this app runs for a server it does not found.
// h is nil until the background start finishes.
type friendHost struct {
	h    *server.Host
	id   string
	path string // the hosting file
	port int

	mu   sync.Mutex // guards file, which the host's goroutines grow
	file hostingFile
	logf func(format string, args ...any)
}

// save writes the hosting file; f.mu is held.
func (f *friendHost) save() {
	if err := saveHostingFile(f.path, f.file); err != nil {
		f.logf("friend host %s: hosting file: %v", shortID(f.id), err)
	}
}

// setKey is HostConfig.OnPasswordKey: it keeps the key the active host
// supplied, so a restart starts with it.
func (f *friendHost) setKey(key []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.file.PasswordKey = hex.EncodeToString(key)
	f.save()
}

// knownHosts is HostConfig.Known.
func (f *friendHost) knownHosts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.file.KnownHosts)
}

// addKnown is HostConfig.SaveKnown, and also what sessions feed it: it adds
// urls to the known hosts and rewrites the hosting file when the set grew.
func (f *friendHost) addKnown(urls []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	merged := state.MergeAddrs(f.file.KnownHosts, urls, state.MaxHosts)
	if len(merged) > len(f.file.KnownHosts) {
		f.file.KnownHosts = merged
		f.save() // best effort: the hosts are learned again
	}
}

// friendStarted is the outcome of a background friend host start.
type friendStarted struct {
	id  string
	f   *friendHost
	h   *server.Host
	err error
}

func (g *game) friendChan() chan friendStarted {
	if g.friendCh == nil {
		g.friendCh = make(chan friendStarted, 4)
	}
	return g.friendCh
}

func (g *game) hostingDir() string {
	return filepath.Join(filepath.Dir(g.hostedFile()), "hosting")
}

func (g *game) hostingPath(id string) string { return filepath.Join(g.hostingDir(), id+".toml") }

// loadHostingFile reads the hosting file of id; without one it is a fresh,
// enabled file with no port yet.
func (g *game) loadHostingFile(id string) (hostingFile, bool) {
	hf := hostingFile{Enabled: true}
	_, err := toml.DecodeFile(g.hostingPath(id), &hf)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			g.logf("hosting file %s: %v", id, err)
		}
		return hostingFile{Enabled: true}, false
	}
	return hf, true
}

func saveHostingFile(path string, hf hostingFile) error {
	b, err := toml.Marshal(hf)
	if err != nil {
		return err
	}
	return writeHosted(path, b)
}

// freeFriendPort is the first free port of the friend range, or 0.
func freeFriendPort() int {
	for p := friendPortMin; p <= friendPortMax; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			_ = ln.Close()
			return p
		}
	}
	return 0
}

// friendNoticeFile marks that the reach notice was shown.
func (g *game) friendNoticeFile() string { return filepath.Join(g.hostingDir(), ".reach-notice") }

// amHost reports whether c's chain lists this app's key as a host.
func (g *game) amHost(c *chain.Chain) bool {
	st := c.State()
	return st.IsHost(g.id.PublicKey())
}

// syncFriendHosting brings the friend host of s's server in line with the
// chain: running while we are a host and hosting is on, stopped otherwise. It
// runs on connect and on every chain event.
func (g *game) syncFriendHosting(s *session) {
	if s.key == hostedKey || s.c == nil {
		return
	}
	id := s.c.ServerID()
	if id == "" || (g.hosted != nil && g.hosted.id == id) { // one host per server in this app
		return
	}
	if !g.amHost(s.c.Chain()) {
		g.stopFriend(id)
		return
	}
	if f := g.friends[id]; f != nil {
		f.addKnown(g.sessionHosts(s))
		return
	}
	if time.Now().Before(g.friendBackoff[id]) { // a failed start is not repeated on every chain event
		return
	}
	hf, _ := g.loadHostingFile(id)
	if !hf.Enabled {
		return
	}
	c, err := chain.FromEntries(s.c.Chain().Entries())
	if err != nil {
		g.logf("friend host %s: chain copy: %v", id, err)
		return
	}
	hf.PasswordKey = hex.EncodeToString(s.c.PasswordKey())
	hf.KnownHosts = state.MergeAddrs(hf.KnownHosts, g.sessionHosts(s), state.MaxHosts)
	if !g.startFriend(id, c, hf) {
		return
	}
	if _, err := os.Stat(g.friendNoticeFile()); err != nil {
		s.st.Status = friendReachNotice
		_ = writeHosted(g.friendNoticeFile(), nil)
	}
}

// sessionHosts are the host URLs s knows for its server.
func (g *game) sessionHosts(s *session) []string {
	out := slices.Clone(s.hint)
	out = append(out, s.url)
	if e, ok := g.entryFor(s.key); ok {
		out = append(out, e.Hosts...)
	}
	return out
}

// startFriend persists c at chains/<id>.jsonl and the hosting file, and starts
// the host in the background (finding this PC's addresses can be slow). It
// reports whether a start is under way.
func (g *game) startFriend(id string, c *chain.Chain, hf hostingFile) bool {
	key, err := hex.DecodeString(hf.PasswordKey)
	if err != nil || (len(key) != 0 && len(key) != 32) {
		g.friendFailed(id, "bad password_key in the hosting file")
		return false
	}
	c, err = reconcileChain(filepath.Join(chainsDir(g.hostedFile()), id+".jsonl"), c)
	if err != nil {
		g.friendFailed(id, "not hosting for a friend: %v", err)
		return false
	}
	if !g.amHost(c) { // the chain on disk is newer and no longer lists us
		return false
	}
	prev := g.friendStop[id] // an earlier host of this server may still hold its port
	pending := false
	if prev != nil {
		select {
		case <-prev:
		default:
			pending = true
		}
	}
	port := hf.Port
	if port == 0 || !(pending || portFree(port)) {
		old := port
		if port = freeFriendPort(); port == 0 {
			g.friendFailed(id, "no free port in %d-%d to host for a friend", friendPortMin, friendPortMax)
			return false
		}
		if old != 0 {
			g.notice = fmt.Sprintf("port %d is taken; hosting for a friend on port %d instead", old, port)
			g.logf("%s", g.notice)
		}
	}
	hf.Port = port
	path := g.hostingPath(id)
	if err := saveHostingFile(path, hf); err != nil {
		g.friendFailed(id, "hosting file: %v", err)
		return false
	}
	hdir := historyDir(g.hostedFile())
	if err := os.MkdirAll(hdir, 0o700); err != nil {
		g.friendFailed(id, "friend history: %v", err)
		return false
	}
	f := &friendHost{id: id, path: path, port: port, file: hf, logf: g.logf}
	if g.friends == nil {
		g.friends = map[string]*friendHost{}
	}
	g.friends[id] = f
	cfg := server.DefaultConfig()
	cfg.Addr = fmt.Sprintf(":%d", port)
	cfg.Discoverable = false // the app's own hosted server owns discovery
	cfg.HostKey, cfg.Chain, cfg.HostName = g.id, c, g.defaultName()
	cfg.PasswordKey = key
	cfg.HistoryPath = filepath.Join(hdir, id+".jsonl")
	serverLogf(&cfg)
	ch := g.friendChan()
	go func() {
		if prev != nil {
			<-prev
		}
		cfg.Advertise = g.advertiseAddrs(port)
		h := server.NewHost(server.HostConfig{
			Server: cfg, Known: f.knownHosts, SaveKnown: f.addKnown, OnPasswordKey: f.setKey,
			Notify: func(msg string) { g.logf("friend host %s: %s", shortID(id), msg) },
		})
		ch <- friendStarted{id: id, f: f, h: h, err: h.Start()}
	}()
	return true
}

// friendBackoffFor is how long a failed friend host start is not retried.
const friendBackoffFor = time.Minute

// friendFailed reports why a friend host did not start, once: the same server
// is not tried again before the backoff ends or the toggle is used.
func (g *game) friendFailed(id, format string, args ...any) {
	g.notice = fmt.Sprintf(format, args...)
	g.logf("friend host %s: %s", shortID(id), g.notice)
	if g.friendBackoff == nil {
		g.friendBackoff = map[string]time.Time{}
	}
	g.friendBackoff[id] = time.Now().Add(friendBackoffFor)
}

// reconcileChain returns the chain to host from: the persisted one at path and
// c, whichever is longer when one is a prefix of the other (the file is then
// written or kept accordingly). A fork is an error and leaves the file alone.
func reconcileChain(path string, c *chain.Chain) (*chain.Chain, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return c, c.SetPath(path)
	}
	local, err := chain.Load(path)
	if err != nil {
		return nil, fmt.Errorf("saved chain unreadable: %w", err)
	}
	le, ce := local.Entries(), c.Entries()
	for i := 0; i < min(len(le), len(ce)); i++ {
		if le[i].Seq != ce[i].Seq || !bytes.Equal(le[i].Sig, ce[i].Sig) {
			return nil, fmt.Errorf("saved chain differs from the server's at entry %d", i)
		}
	}
	if len(le) >= len(ce) {
		return local, nil
	}
	return c, c.SetPath(path)
}

func portFree(p int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// applyFriend records a finished background start. A host stopped while it
// started is shut down again.
func (g *game) applyFriend(r friendStarted) {
	if r.err != nil {
		if g.friends[r.id] == r.f {
			delete(g.friends, r.id)
		}
		g.friendFailed(r.id, "can't host for a friend: %v", r.err)
		return
	}
	if g.friends[r.id] != r.f {
		g.shutdownAsync(r.id, r.h)
		return
	}
	r.f.h = r.h
	g.logf("hosting %s for a friend on port %d", shortID(r.id), r.f.port)
}

func shutdownHost(h *server.Host) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.Shutdown(ctx)
}

// shutdownHostFn is a seam for tests.
var shutdownHostFn = shutdownHost

// shutdownAsync shuts h down in the background; friendStop[id] closes once it
// has released its port, and a later host of id waits for that.
func (g *game) shutdownAsync(id string, h *server.Host) {
	if g.friendStop == nil {
		g.friendStop = map[string]chan struct{}{}
	}
	prev, done := g.friendStop[id], make(chan struct{})
	g.friendStop[id] = done
	g.friendWG.Add(1)
	go func() {
		defer g.friendWG.Done()
		defer close(done)
		if prev != nil {
			<-prev
		}
		shutdownHostFn(h)
	}()
}

// stopFriend stops the friend host of id in the background, keeping its files.
func (g *game) stopFriend(id string) {
	f := g.friends[id]
	if f == nil {
		return
	}
	delete(g.friends, id)
	if f.h != nil {
		g.shutdownAsync(id, f.h)
	}
}

// stopFriends stops every friend host (app quit), waiting at most 3 s in all.
func (g *game) stopFriends() {
	for id := range g.friends {
		g.stopFriend(id)
	}
	done := make(chan struct{})
	go func() {
		g.friendWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(friendStopWait):
	}
}

// bootFriends starts every enabled friend host whose chain still lists us.
// Call it after bootHosted.
func (g *game) bootFriends() {
	ents, err := os.ReadDir(g.hostingDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || (g.hosted != nil && g.hosted.id == id) {
			continue
		}
		hf, ok := g.loadHostingFile(id)
		if !ok || !hf.Enabled {
			continue
		}
		c, err := chain.Load(filepath.Join(chainsDir(g.hostedFile()), id+".jsonl"))
		if err != nil || !g.amHost(c) {
			continue
		}
		g.startFriend(id, c, hf)
	}
}

// friendMenuItem is the tile menu's hosting toggle for the server at key: shown
// while connected to a server we are a host of (and not the founder-hosted one).
func (g *game) friendMenuItem(key string) (menuItem, bool) {
	s := g.sess[key]
	if s == nil || s.c == nil || key == hostedKey || !g.amHost(s.c.Chain()) {
		return menuItem{}, false
	}
	id := s.c.ServerID()
	if id == "" || (g.hosted != nil && g.hosted.id == id) {
		return menuItem{}, false
	}
	hf, _ := g.loadHostingFile(id)
	st := "off"
	if hf.Enabled {
		st = "on"
	}
	return menuItem{label: "let my PC host this server: " + st, act: func() { g.setFriendHosting(s, !hf.Enabled) }}, true
}

// setFriendHosting turns hosting for s's server on or off and applies it.
func (g *game) setFriendHosting(s *session, on bool) {
	id := s.c.ServerID()
	delete(g.friendBackoff, id) // an explicit switch-on tries again
	hf, _ := g.loadHostingFile(id)
	hf.Enabled = on
	if f := g.friends[id]; f != nil {
		f.mu.Lock()
		f.file.Enabled = on
		hf = f.file
		f.mu.Unlock()
	}
	if err := saveHostingFile(g.hostingPath(id), hf); err != nil {
		g.notice = fmt.Sprintf("hosting file: %v", err)
		return
	}
	if !on {
		g.stopFriend(id)
		return
	}
	g.syncFriendHosting(s)
}
