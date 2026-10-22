package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// History is the host's chat history store: per-channel messages kept in
// memory, mirrored to an append-only JSON-lines file. Safe for concurrent use.
type History struct {
	mu      sync.Mutex
	path    string // "" = memory only
	days    int
	mb      int
	chans   map[string][]proto.ChatMessage // sorted by (TS, ID)
	ids     map[string]lineMeta            // id -> where and how big the stored line is
	size    int
	skipped int
	needNL  bool     // file does not end in a newline; next append starts a fresh line
	f       *os.File // open append handle on path; nil until the first Add
	fileLen int      // bytes in the file, stale lines and all
	closed  bool     // Close was called; Add fails

	// rewriteMu serializes rewrites. The rest is guarded by mu: while a
	// rewrite writes its temp file without mu, rewriting is set and Add keeps
	// the lines it appends in pending; gen changes when the store is emptied.
	rewriteMu sync.Mutex
	rewriting bool
	pending   []byte
	gen       int
	retryAt   time.Time // Compact leaves the file alone until then

	// write appends b to the open store file; nil means f.Write. Tests set it
	// to force a failed or partial write.
	write func(f *os.File, b []byte) (int, error)
	// tmpWrite writes a rewrite's temp file and rename moves it into place;
	// nil means f.Write and os.Rename. Tests set them to slow or fail a rewrite.
	tmpWrite func(f *os.File, b []byte) (int, error)
	rename   func(oldpath, newpath string) error
}

// lineMeta is what the store remembers about each line by id.
type lineMeta struct {
	n  int       // encoded line length, newline included
	ts time.Time // lets Page place an id without scanning
}

// Newest returns the newest stored timestamp across channels; zero if empty.
func (h *History) Newest() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	var t time.Time
	for _, s := range h.chans {
		if len(s) > 0 && s[len(s)-1].TS.After(t) {
			t = s[len(s)-1].TS
		}
	}
	return t
}

// tempPattern is the os.CreateTemp pattern rewrite uses for base.
func tempPattern(base string) string { return base + ".tmp*" }

// removeStaleTemps deletes temp files a crashed rewrite left beside path:
// exactly base + ".tmp" + the digits os.CreateTemp puts in place of "*".
func removeStaleTemps(path string) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	prefix := strings.TrimSuffix(tempPattern(base), "*")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok || rest == "" || e.IsDir() || strings.TrimLeft(rest, "0123456789") != "" {
			continue
		}
		os.Remove(filepath.Join(dir, name))
	}
}

// OpenHistory loads the store at path. "" keeps history in memory only. A
// missing file is empty; malformed lines are skipped and counted.
func OpenHistory(path string) (*History, error) {
	h := &History{
		path:  path,
		days:  DefaultHistoryDays,
		mb:    DefaultHistoryMB,
		chans: map[string][]proto.ChatMessage{},
		ids:   map[string]lineMeta{},
	}
	if path == "" {
		return h, nil
	}
	removeStaleTemps(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	h.needNL = len(data) > 0 && data[len(data)-1] != '\n'
	h.fileLen = len(data)
	for len(data) > 0 {
		var raw []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			raw, data = data[:i], data[i+1:]
		} else {
			raw, data = data, nil
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var m proto.ChatMessage
		if json.Unmarshal(raw, &m) != nil || m.ID == "" || m.Channel == "" {
			h.skipped++
			continue
		}
		if _, dup := h.ids[m.ID]; dup {
			continue
		}
		h.insert(m, len(raw)+1)
	}
	return h, nil
}

// less orders messages by (TS, ID).
func histLess(a, b proto.ChatMessage) bool {
	if !a.TS.Equal(b.TS) {
		return a.TS.Before(b.TS)
	}
	return a.ID < b.ID
}

// insert places m in its channel in order. Caller holds mu (or is Open).
func (h *History) insert(m proto.ChatMessage, n int) {
	s := h.chans[m.Channel]
	i := sort.Search(len(s), func(i int) bool { return histLess(m, s[i]) })
	s = append(s, proto.ChatMessage{})
	copy(s[i+1:], s[i:])
	s[i] = m
	h.chans[m.Channel] = s
	h.ids[m.ID] = lineMeta{n, m.TS}
	h.size += n
}

// Skipped reports how many malformed lines Open ignored (cleared by a rewrite).
func (h *History) Skipped() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.skipped
}

// SetLimits sets the day and megabyte caps applied by Add and Trim.
func (h *History) SetLimits(days, mb int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.days, h.mb = days, mb
}

// Size is the number of bytes of stored lines, newlines included.
func (h *History) Size() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.size
}

// encodeLine is m as one JSON line, newline included. HTML is left unescaped:
// escaping would turn each '<' into six bytes and inflate the stored size.
func encodeLine(m proto.ChatMessage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// errClosed is returned by Add once the store has been closed.
var errClosed = errors.New("history: store is closed")

// Add stores m and appends it to the file (no fsync). It is a no-op while the
// day cap is 0, and ignores an id already stored. If m pushes the store past
// the megabyte cap, the oldest messages leave memory at once; the file is
// shortened by Compact.
func (h *History) Add(m proto.ChatMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errClosed
	}
	if h.days == 0 {
		return nil
	}
	if _, dup := h.ids[m.ID]; dup {
		return nil
	}
	b, err := encodeLine(m)
	if err != nil {
		return err
	}
	if h.path != "" {
		if err := h.appendLocked(b); err != nil {
			return err
		}
	}
	h.insert(m, len(b))
	if limit := h.mb << 20; h.size > limit {
		drop, size := h.plan(time.Time{}, limit)
		h.evict(drop, size)
	}
	return nil
}

// evict removes the first drop[ch] messages of each channel in place and sets
// the stored size to size (what plan computed). Caller holds mu.
func (h *History) evict(drop map[string]int, size int) {
	for ch, k := range drop {
		if k == 0 {
			continue
		}
		s := h.chans[ch]
		for i := range s[:k] {
			delete(h.ids, s[i].ID)
			s[i] = proto.ChatMessage{} // let the text go
		}
		if k == len(s) {
			delete(h.chans, ch)
		} else {
			h.chans[ch] = s[k:]
		}
	}
	h.size = size
}

// appendLocked appends line b to the store file through the open handle.
func (h *History) appendLocked(b []byte) error {
	if h.f == nil {
		f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		h.f = f
	}
	out := b
	if h.needNL {
		out = append([]byte{'\n'}, b...)
	}
	write := h.write
	if write == nil {
		write = (*os.File).Write
	}
	n, err := write(h.f, out)
	h.fileLen += n
	if err != nil {
		// Part of the line may be in the file; start the next one fresh, on a
		// fresh handle.
		h.needNL = true
		h.closeFile()
		return err
	}
	h.needNL = false
	if h.rewriting {
		h.pending = append(h.pending, b...) // the rewrite must carry it over
	}
	return nil
}

// closeFile closes the append handle; the next Add reopens it. Caller holds mu.
func (h *History) closeFile() error {
	if h.f == nil {
		return nil
	}
	err := h.f.Close()
	h.f = nil
	return err
}

// Close releases the store file for good: later Adds fail, and a rewrite in
// flight is abandoned. Page still answers from memory.
func (h *History) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	return h.closeFile()
}

// Page returns up to n messages of channel, oldest first: the newest when
// beforeID is empty, else those strictly older than beforeID in (TS, ID)
// order. A stored beforeID supplies its own TS; for one the store no longer
// holds, beforeTS stands in when non-zero. more reports whether older
// messages remain. An unknown beforeID without beforeTS yields nil, false.
func (h *History) Page(channel, beforeID string, beforeTS time.Time, n int) (msgs []proto.ChatMessage, more bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.chans[channel]
	end := len(s)
	if beforeID != "" {
		if meta, ok := h.ids[beforeID]; ok {
			beforeTS = meta.ts
		} else if beforeTS.IsZero() {
			return nil, false
		}
		key := proto.ChatMessage{ID: beforeID, TS: beforeTS}
		end = sort.Search(len(s), func(i int) bool { return !histLess(s[i], key) })
	}
	if n < 0 {
		n = 0
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	if start < end {
		msgs = append([]proto.ChatMessage(nil), s[start:end]...)
	}
	return msgs, start > 0
}

// plan decides what to drop: messages older than cutoff, then the globally
// oldest ones until the stored size fits limit. It returns the number of
// leading messages to drop from each channel and the size that leaves.
func (h *History) plan(cutoff time.Time, limit int) (drop map[string]int, size int) {
	drop = make(map[string]int, len(h.chans))
	size = h.size
	for ch, s := range h.chans {
		k := sort.Search(len(s), func(i int) bool { return !s[i].TS.Before(cutoff) })
		drop[ch] = k
		for _, m := range s[:k] {
			size -= h.ids[m.ID].n
		}
	}
	for size > limit {
		var oldest *proto.ChatMessage
		var oldestCh string
		for ch, s := range h.chans {
			if drop[ch] >= len(s) {
				continue
			}
			if m := &s[drop[ch]]; oldest == nil || histLess(*m, *oldest) {
				oldest, oldestCh = m, ch
			}
		}
		if oldest == nil {
			break
		}
		size -= h.ids[oldest.ID].n
		drop[oldestCh]++
	}
	return drop, size
}

// Trim drops messages older than the day cap, then the globally oldest ones
// until Size() fits the megabyte cap, and rewrites the file if anything was
// dropped or skipped. With a day cap of 0 it empties the store and removes the
// file. Memory is trimmed first; if the rewrite fails the file keeps its old
// lines until a later rewrite and the error is returned.
func (h *History) Trim(now time.Time) error {
	h.rewriteMu.Lock()
	defer h.rewriteMu.Unlock()
	return h.trim(now, false)
}

// Compact is Trim for the file only: when the file has grown past twice the
// megabyte cap (Add drops lines from memory but only appends to the file) it
// is rewritten down to the live messages. Otherwise, while another rewrite
// runs, or for a minute after a failed one, it does nothing, cheaply.
func (h *History) Compact(now time.Time) error {
	if !h.rewriteMu.TryLock() {
		return nil
	}
	defer h.rewriteMu.Unlock()
	return h.trim(now, true)
}

// retryAfter is how long Compact leaves a failed rewrite alone.
const retryAfter = time.Minute

// snapLine is one message copied for a rewrite, with its size when copied.
type snapLine struct {
	m proto.ChatMessage
	n int
}

// trim does Trim, or Compact when compact is set. Caller holds rewriteMu.
func (h *History) trim(now time.Time, compact bool) error {
	h.mu.Lock()
	if compact && (h.path == "" || h.closed || h.fileLen <= 2*h.mb<<20 || now.Before(h.retryAt)) {
		h.mu.Unlock()
		return nil
	}
	if h.days == 0 {
		// Clear memory first so nothing old is served even if the remove fails.
		h.chans = map[string][]proto.ChatMessage{}
		h.ids = map[string]lineMeta{}
		h.size, h.skipped, h.needNL, h.fileLen = 0, 0, false, 0
		h.gen++ // a rewrite that began earlier must not bring the file back
		var err error
		if h.path != "" {
			h.closeFile() // Windows cannot remove an open file
			if err = os.Remove(h.path); errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		h.mu.Unlock()
		return err
	}

	cutoff := now.Add(-time.Duration(h.days) * 24 * time.Hour)
	drop, size := h.plan(cutoff, h.mb<<20)
	dropped := 0
	for _, k := range drop {
		dropped += k
	}
	if dropped == 0 && h.skipped == 0 && !compact {
		h.mu.Unlock()
		return nil
	}
	h.evict(drop, size)
	if h.path == "" {
		h.skipped = 0
		h.mu.Unlock()
		return nil
	}
	var snap []snapLine
	for _, s := range h.chans {
		for _, m := range s {
			snap = append(snap, snapLine{m, h.ids[m.ID].n})
		}
	}
	h.rewriting, h.pending = true, nil
	gen, write := h.gen, h.tmpWrite
	h.mu.Unlock()

	return h.rewrite(snap, gen, write, now)
}

// rewrite writes snap, in global (TS, ID) order, to a temp file beside the
// store without holding mu, then takes mu to add the lines Add appended in the
// meantime and rename the file over the store. The open append handle is
// closed first (the rename fails over it on Windows); the next Add reopens
// the new file. Caller holds rewriteMu, so only one rewrite runs at a time.
func (h *History) rewrite(snap []snapLine, gen int, write func(*os.File, []byte) (int, error), now time.Time) error {
	sort.Slice(snap, func(i, j int) bool { return histLess(snap[i].m, snap[j].m) })
	var buf bytes.Buffer
	var resized []snapLine // lines whose re-encoded length differs
	var err error
	for _, l := range snap {
		var b []byte
		if b, err = encodeLine(l.m); err != nil {
			break
		}
		buf.Write(b)
		if len(b) != l.n {
			resized = append(resized, snapLine{l.m, len(b)})
		}
	}
	var tmp string
	if err == nil {
		tmp, err = h.writeTemp(buf.Bytes(), write)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.pending
	h.rewriting, h.pending = false, nil
	if err == nil && (h.gen != gen || h.closed) {
		os.Remove(tmp) // the store was emptied or closed meanwhile
		return nil
	}
	if err == nil && len(pending) > 0 {
		err = appendTemp(tmp, pending)
	}
	if err == nil {
		h.closeFile()
		rename := h.rename
		if rename == nil {
			rename = os.Rename
		}
		err = rename(tmp, h.path)
	}
	if err != nil {
		if tmp != "" {
			os.Remove(tmp)
		}
		h.retryAt = now.Add(retryAfter)
		return err
	}
	h.fileLen = buf.Len() + len(pending)
	h.skipped, h.needNL = 0, false
	for _, l := range resized {
		if meta, ok := h.ids[l.m.ID]; ok {
			h.size += l.n - meta.n
			h.ids[l.m.ID] = lineMeta{l.n, meta.ts}
		}
	}
	return nil
}

// writeTemp writes data to a fresh synced temp file beside the store and
// returns its name; on error the file is gone.
func (h *History) writeTemp(data []byte, write func(*os.File, []byte) (int, error)) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(h.path), tempPattern(filepath.Base(h.path)))
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if write == nil {
		write = (*os.File).Write
	}
	_, werr := write(f, data)
	if werr == nil {
		werr = f.Sync() // the rename must not land before the data
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return "", werr
	}
	return tmp, nil
}

// appendTemp appends data to the temp file at path and syncs it.
func appendTemp(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// NewestBefore returns the newest stored timestamp that is not after limit;
// zero if there is none.
func (h *History) NewestBefore(limit time.Time) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	var t time.Time
	for _, s := range h.chans {
		i := sort.Search(len(s), func(i int) bool { return s[i].TS.After(limit) })
		if i > 0 && s[i-1].TS.After(t) {
			t = s[i-1].TS
		}
	}
	return t
}
