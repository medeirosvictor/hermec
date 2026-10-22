package server

import (
	"context"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// loses is the yield rule: fewer voice members yields; on a tie fewer clients
// yields; on a tie the larger priority number yields.
func loses(voice, clients, prio int, o proto.YieldLoad) bool {
	if voice != o.VoiceMembers {
		return voice < o.VoiceMembers
	}
	if clients != o.Clients {
		return clients < o.Clients
	}
	return prio > o.Priority
}

func replyLoad(r proto.LinkProbeReply) proto.YieldLoad {
	return proto.YieldLoad{VoiceMembers: r.VoiceMembers, Clients: r.Clients, Priority: r.Priority}
}

// hostOnChain reports whether the host with this fingerprint is a host on our
// chain.
func (h *Host) hostOnChain(fp string) bool {
	st := h.srv.chain.State()
	for _, hi := range st.Hosts {
		if hi.Fingerprint == fp {
			return true
		}
	}
	return false
}

// iLose reports whether, with our load as it is now, we yield to r.
func (h *Host) iLose(r proto.LinkProbeReply) bool {
	voice, clients := h.srv.load()
	return loses(voice, clients, h.srv.selfInfo().Priority, replyLoad(r))
}

// probeAndYield looks for another active host. If there is one we lose to, it
// merges into it; demoted reports that the host left the active role, and l is
// then the link to the winner (nil if none could be made and the host became
// active again).
func (h *Host) probeAndYield() (l *Link, demoted bool) {
	urls := h.targets(nil)
	if len(urls) == 0 {
		return nil, false
	}
	timeout := probeEvery
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()
	var winURL string
	var win proto.LinkProbeReply
	h.probeEach(ctx, urls, func(url string, r proto.LinkProbeReply) bool {
		// A winner we do not know as a host of our chain is not one to yield to.
		if !h.sameServer(r) || !h.hostOnChain(r.Self.Fingerprint) || !h.iLose(r) {
			return false
		}
		winURL, win = url, r
		return true
	})
	if winURL == "" {
		return nil, false
	}
	voice, clients := h.srv.load()
	h.srv.logf("server: %s (load %d/%d) yields to %s (load %d/%d)", h.srv.cfg.HostName, voice, clients, win.Self.Name, win.VoiceMembers, win.Clients)
	return h.yield(winURL, win)
}

// commonPrefix is how many leading entries of a and b are the same.
func commonPrefix(a, b []chain.Entry) int {
	n := 0
	for n < len(a) && n < len(b) && a[n].Hash() == b[n].Hash() {
		n++
	}
	return n
}

// yield merges this host into the winner of a split brain: it sends its chat
// history, tells its clients to move, tells those who lost a chain change, takes
// the winner's chain and links to it. A winner whose chain is a prefix of ours
// is sent our extra entries instead (nothing was lost on either side). It
// reports false, with the host still active, if the merge is called off.
func (h *Host) yield(url string, win proto.LinkProbeReply) (*Link, bool) {
	dctx, cancel := context.WithTimeout(h.ctx, 2*dialTimeout)
	ws, theirs, err := dialMerge(dctx, url, h.srv)
	cancel()
	if err != nil {
		h.srv.logf("server: merge into %s: %v", url, err)
		return nil, false
	}
	defer ws.Close()
	defer watchClose(h.ctx, ws)()
	full := append(h.srv.chain.Entries()[:1:1], theirs...)
	if _, err := chain.FromEntries(full); err != nil {
		h.srv.logf("server: merge into %s: its chain does not verify: %v", url, err)
		return nil, false
	}
	if beforeYieldRecheck != nil {
		beforeYieldRecheck(h)
	}
	// Loads change: look again, right before committing.
	pctx, pcancel := context.WithTimeout(h.ctx, dialTimeout)
	r, err := ProbeHost(pctx, url, h.srv)
	pcancel()
	if err != nil || !h.sameServer(r) || !h.iLose(r) {
		h.srv.logf("server: merge into %s called off: it no longer wins", url)
		return nil, false
	}

	// From here the host is no longer active: no chat or chain change lands
	// after what is read below.
	promoted := h.srv.promoted.Load()
	h.srv.goIdle()
	if beforeMergeHistory != nil {
		beforeMergeHistory(h)
	}
	mine := h.srv.chain.Entries()
	fastForward := commonPrefix(mine, full) == len(full) && len(mine) > len(full)
	if fastForward {
		if err := sendChainEntries(ws, mine[len(full):]); err != nil {
			h.srv.resumeActive(promoted)
			h.srv.logf("server: merge into %s: %v", url, err)
			return nil, false
		}
	}
	if err := h.srv.sendMergeHistory(ws); err != nil {
		h.srv.resumeActive(promoted)
		h.srv.logf("server: merge into %s: %v", url, err)
		return nil, false
	}
	var lost map[string]bool
	if !fastForward {
		lost = h.srv.lostAuthors(full)
	}
	h.srv.Demote(win.Self, "merge", lost)
	if !fastForward {
		if err := h.srv.replaceChain(full); err != nil {
			h.srv.logf("server: merge into %s: taking its chain: %v", url, err)
		}
	}
	h.learn(win.Self.Addrs...)
	h.setRole(RoleIdle, "")
	return h.relink(win.Self, []string{url}, true), true
}

// sendChainEntries sends entries to the host on ws as chain frames.
func sendChainEntries(ws *websocket.Conn, entries []chain.Entry) error {
	frames, err := chainFrames(entries)
	if err != nil {
		return err
	}
	for _, f := range frames {
		_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := ws.WriteMessage(websocket.TextMessage, f); err != nil {
			return err
		}
	}
	return nil
}

// relink links to the host that is now active, retrying briefly; with none, it
// cold-starts (which makes this host active when nobody answers).
func (h *Host) relink(to proto.HostInfo, urls []string, full bool) *Link {
	urls = append(append([]string(nil), urls...), to.Addrs...)
	for attempt := 0; attempt < 10 && !h.stopped(); attempt++ {
		for _, u := range urls {
			dctx, cancel := context.WithTimeout(h.ctx, dialTimeout)
			l, err := dialLinkOpts(dctx, u, h.srv, full)
			cancel()
			if err == nil {
				h.mu.Lock()
				h.active = to
				h.mu.Unlock()
				h.install(l)
				return l
			}
		}
		select {
		case <-h.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return h.rejoin(urls, coldStartWindow)
}

// handbackCandidate is the linked host with the best priority among those
// that outrank this host, or nil.
func (h *Host) handbackCandidate() *LinkPeer {
	mine := h.srv.selfInfo().Priority
	for _, p := range h.srv.Links() { // best priority first
		if p.Priority > 0 && p.Priority < mine && len(p.Addrs) > 0 {
			return &p
		}
	}
	return nil
}

// handback hands the active role to p. The host stops accepting writes first,
// so nothing lands here after p took over; then it sends handover, waits for p
// to report itself active, moves clients to it and links to it as idle or
// standby. It reports false, with the host serving again, if p never became
// active.
func (h *Host) handback(p *LinkPeer) (*Link, bool) {
	promoted := h.srv.promoted.Load()
	h.srv.goIdle()
	if !h.srv.sendHandover(p.Fingerprint) || !h.waitActive(p.Addrs) {
		h.srv.resumeActive(promoted)
		return nil, false
	}
	to := proto.HostInfo{Fingerprint: p.Fingerprint, Name: p.Name, Addrs: p.Addrs, Priority: p.Priority}
	h.srv.Demote(to, "handback", nil)
	h.setRole(RoleIdle, "")
	return h.relink(to, nil, false), true
}
