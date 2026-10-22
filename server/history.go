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
	needNL  bool // file does not end in a newline; next append starts a fresh line

	// write appends b to the open store file; nil means f.Write. Tests set it
	// to force a failed or partial write.
	write func(f *os.File, b []byte) (int, error)
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

// Add stores m and appends it to the file (no fsync). It is a no-op while the
// day cap is 0, and ignores an id already stored.
func (h *History) Add(m proto.ChatMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.days == 0 {
		return nil
	}
	if _, dup := h.ids[m.ID]; dup {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if h.path != "" {
		out := b
		if h.needNL {
			out = append([]byte{'\n'}, b...)
		}
		f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		write := h.write
		if write == nil {
			write = (*os.File).Write
		}
		_, werr := write(f, out)
		cerr := f.Close()
		if werr == nil {
			werr = cerr
		}
		if werr != nil {
			// Part of the line may be in the file; start the next one fresh.
			h.needNL = true
			return werr
		}
		h.needNL = false
	}
	h.insert(m, len(b))
	return nil
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

// Trim drops messages older than the day cap, then the globally oldest ones
// until Size() fits the megabyte cap, and rewrites the file if anything was
// dropped or skipped. With a day cap of 0 it empties the store and removes the
// file. On a rewrite error the in-memory state is left as it was.
func (h *History) Trim(now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.days == 0 {
		// Clear memory first so nothing old is served even if the remove fails.
		h.chans = map[string][]proto.ChatMessage{}
		h.ids = map[string]lineMeta{}
		h.size, h.skipped, h.needNL = 0, 0, false
		if h.path != "" {
			if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}

	cutoff := now.Add(-time.Duration(h.days) * 24 * time.Hour)
	// heads[ch] = number of leading messages dropped from each channel.
	drop := map[string]int{}
	size := h.size
	for ch, s := range h.chans {
		k := sort.Search(len(s), func(i int) bool { return !s[i].TS.Before(cutoff) })
		drop[ch] = k
		for _, m := range s[:k] {
			size -= h.ids[m.ID].n
		}
	}
	limit := h.mb << 20
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

	dropped := 0
	for _, k := range drop {
		dropped += k
	}
	if dropped == 0 && h.skipped == 0 {
		return nil
	}

	kept := make(map[string][]proto.ChatMessage, len(h.chans))
	for ch, s := range h.chans {
		if rest := s[drop[ch]:]; len(rest) > 0 {
			kept[ch] = append([]proto.ChatMessage(nil), rest...)
		}
	}
	if h.path != "" {
		if err := h.rewrite(kept); err != nil {
			return err
		}
	}
	ids := make(map[string]lineMeta, len(h.ids))
	for _, s := range kept {
		for _, m := range s {
			ids[m.ID] = h.ids[m.ID]
		}
	}
	h.chans, h.ids, h.size = kept, ids, size
	h.skipped, h.needNL = 0, false
	return nil
}

// rewrite writes msgs to a temp file beside the store and renames it over the
// store, in global (TS, ID) order. Caller holds mu.
func (h *History) rewrite(msgs map[string][]proto.ChatMessage) error {
	var all []proto.ChatMessage
	for _, s := range msgs {
		all = append(all, s...)
	}
	sort.Slice(all, func(i, j int) bool { return histLess(all[i], all[j]) })

	f, err := os.CreateTemp(filepath.Dir(h.path), tempPattern(filepath.Base(h.path)))
	if err != nil {
		return err
	}
	tmp := f.Name()
	var buf bytes.Buffer
	for _, m := range all {
		b, err := json.Marshal(m)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_, werr := f.Write(buf.Bytes())
	if werr == nil {
		werr = f.Sync() // the rename must not land before the data
	}
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, h.path)
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	return nil
}
