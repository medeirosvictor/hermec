package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

var histBase = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func hmsg(ch string, i int, ts time.Time) proto.ChatMessage {
	return proto.ChatMessage{
		ID:      fmt.Sprintf("%032x", i),
		Channel: ch,
		From:    proto.Member{Fingerprint: "fp", Name: "bob"},
		Text:    fmt.Sprintf("msg %d", i),
		TS:      ts,
	}
}

func openH(t *testing.T, path string) *History {
	t.Helper()
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() }) // an open handle would block removing the temp dir
	return h
}

func line(t *testing.T, m proto.ChatMessage) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestHistoryAddReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	// added out of order to exercise sorting
	h.Add(hmsg("general", 2, histBase.Add(2*time.Second)))
	h.Add(hmsg("random", 3, histBase.Add(3*time.Second)))
	h.Add(hmsg("general", 1, histBase.Add(time.Second)))
	h2 := openH(t, p)
	got, more := h2.Page("general", "", time.Time{}, 10)
	if more || len(got) != 2 || got[0].ID != hmsg("", 1, time.Time{}).ID || got[1].ID != hmsg("", 2, time.Time{}).ID {
		t.Fatalf("got %+v more=%v", got, more)
	}
	if r, _ := h2.Page("random", "", time.Time{}, 10); len(r) != 1 {
		t.Fatalf("random: %+v", r)
	}
}

func TestHistoryPaging(t *testing.T) {
	h := openH(t, filepath.Join(t.TempDir(), "h.jsonl"))
	for i := 0; i < 25; i++ {
		h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
	}
	p1, more := h.Page("c", "", time.Time{}, 10)
	if !more || len(p1) != 10 || p1[0].Text != "msg 15" || p1[9].Text != "msg 24" {
		t.Fatalf("p1 %v more=%v", p1, more)
	}
	p2, more := h.Page("c", p1[0].ID, time.Time{}, 10)
	if !more || len(p2) != 10 || p2[0].Text != "msg 5" || p2[9].Text != "msg 14" {
		t.Fatalf("p2 %v more=%v", p2, more)
	}
	p3, more := h.Page("c", p2[0].ID, time.Time{}, 10)
	if more || len(p3) != 5 || p3[0].Text != "msg 0" {
		t.Fatalf("p3 %v more=%v", p3, more)
	}
	if m, more := h.Page("c", "nope", time.Time{}, 10); m != nil || more {
		t.Fatalf("unknown: %v %v", m, more)
	}
}

// A page request with a known id works on a big store, and walking back from
// the newest line reaches every line exactly once.
func TestHistoryPageLargeStore(t *testing.T) {
	h := openH(t, "")
	const total = 5000
	for i := 0; i < total; i++ {
		h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Millisecond)))
	}
	seen, before := 0, ""
	for {
		page, more := h.Page("c", before, time.Time{}, 100)
		if len(page) > 0 && seen+len(page) <= total && page[len(page)-1].Text != fmt.Sprintf("msg %d", total-seen-1) {
			t.Fatalf("page after %d lines ends at %q", seen, page[len(page)-1].Text)
		}
		seen += len(page)
		if !more {
			break
		}
		before = page[0].ID
	}
	if seen != total {
		t.Fatalf("paged %d of %d lines", seen, total)
	}
}

// An id the store no longer holds is placed by the ts the client sends.
func TestHistoryPageUnknownIDWithTS(t *testing.T) {
	h := openH(t, "")
	for i := 0; i < 10; i++ {
		h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
	}
	gone := hmsg("c", 100, histBase.Add(5*time.Second+time.Millisecond)) // between msg 5 and 6, never stored
	page, more := h.Page("c", gone.ID, gone.TS, 3)
	if !more || len(page) != 3 || page[0].Text != "msg 3" || page[2].Text != "msg 5" {
		t.Fatalf("page = %v more=%v", page, more)
	}
	// Equal ts: ordered by id, as everywhere else.
	page, _ = h.Page("c", hmsg("c", 5, time.Time{}).ID[:31]+"~", histBase.Add(5*time.Second), 3)
	if len(page) != 3 || page[2].Text != "msg 5" {
		t.Fatalf("tie page = %v", page)
	}
	// Known id wins over a stale ts.
	known := hmsg("c", 7, time.Time{})
	page, _ = h.Page("c", known.ID, histBase, 2)
	if len(page) != 2 || page[1].Text != "msg 6" {
		t.Fatalf("known id page = %v", page)
	}
}

func TestHistoryOrderTiesByID(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 9, histBase))
	h.Add(hmsg("c", 3, histBase))
	got, _ := h.Page("c", "", time.Time{}, 10)
	if len(got) != 2 || got[0].ID > got[1].ID {
		t.Fatalf("got %+v", got)
	}
}

func TestOpenSkipsMalformedLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	body := line(t, hmsg("c", 1, histBase)) + "{\"broken\n" + line(t, hmsg("c", 2, histBase.Add(time.Second))) + `{"id":"abc","channel":"c","te`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h := openH(t, p)
	if got, _ := h.Page("c", "", time.Time{}, 10); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if h.Skipped() != 2 {
		t.Fatalf("skipped %d", h.Skipped())
	}
	h.SetLimits(30, 8)
	if err := h.Trim(histBase); err != nil {
		t.Fatal(err)
	}
	if s := openH(t, p).Skipped(); s != 0 {
		t.Fatalf("after trim skipped %d", s)
	}
}

func TestOpenSkipsEmptyIDOrChannel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	noID := hmsg("c", 1, histBase)
	noID.ID = ""
	noCh := hmsg("", 2, histBase)
	os.WriteFile(p, []byte(line(t, noID)+line(t, noCh)+line(t, hmsg("c", 3, histBase))), 0o600)
	h := openH(t, p)
	if h.Skipped() != 2 {
		t.Fatalf("skipped %d", h.Skipped())
	}
}

func TestOpenDedupesIDs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	a := hmsg("c", 1, histBase)
	b := a
	b.Text = "second"
	os.WriteFile(p, []byte(line(t, a)+line(t, b)), 0o600)
	h := openH(t, p)
	got, _ := h.Page("c", "", time.Time{}, 10)
	if len(got) != 1 || got[0].Text != a.Text {
		t.Fatalf("got %+v", got)
	}
}

func TestTrimDays(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 8)
	now := histBase
	h.Add(hmsg("c", 1, now.Add(-31*24*time.Hour)))
	h.Add(hmsg("c", 2, now.Add(-29*24*time.Hour)))
	if err := h.Trim(now); err != nil {
		t.Fatal(err)
	}
	got, _ := h.Page("c", "", time.Time{}, 10)
	if len(got) != 1 || got[0].Text != "msg 2" {
		t.Fatalf("got %+v", got)
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "msg 1") || !strings.Contains(string(data), "msg 2") {
		t.Fatalf("file: %s", data)
	}
	if h.Size() != len(data) {
		t.Fatalf("size %d vs %d", h.Size(), len(data))
	}
}

func TestTrimSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 8)
	pad := strings.Repeat("x", 1000)
	n := 1500
	for i := 0; i < n; i++ {
		m := hmsg([]string{"a", "b"}[i%2], i, histBase.Add(time.Duration(i)*time.Second))
		m.Text = pad
		h.Add(m)
	}
	h.SetLimits(30, 1) // the cap shrinks after the lines are in
	if h.Size() <= 1<<20 {
		t.Fatalf("setup too small: %d", h.Size())
	}
	if err := h.Trim(histBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if h.Size() > 1<<20 {
		t.Fatalf("size %d", h.Size())
	}
	a, _ := h.Page("a", "", time.Time{}, 5000)
	b, _ := h.Page("b", "", time.Time{}, 5000)
	if len(a)+len(b) < 900 {
		t.Fatalf("dropped too many: %d", len(a)+len(b))
	}
	if a[len(a)-1].ID != hmsg("", n-2, time.Time{}).ID || b[len(b)-1].ID != hmsg("", n-1, time.Time{}).ID {
		t.Fatal("newest not kept")
	}
	if a[0].TS.Before(histBase.Add(400 * time.Second)) {
		t.Fatalf("oldest not dropped first")
	}
	fi, _ := os.Stat(p)
	if int(fi.Size()) != h.Size() {
		t.Fatalf("file %d size %d", fi.Size(), h.Size())
	}
}

// Add alone keeps memory and file within the cap, with no Trim in between.
func TestAddEnforcesCapBetweenTrims(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 1)
	const limit = 1 << 20
	// '<' would take six bytes each if the line encoder escaped it.
	pad := strings.Repeat("<", 4000)
	var maxLine, maxFile int
	n := 2000 // about 8 MB of lines, eight times the cap
	for i := 0; i < n; i++ {
		m := hmsg("c", i, histBase.Add(time.Duration(i)*time.Second))
		m.Text = pad
		if err := h.Add(m); err != nil {
			t.Fatal(err)
		}
		// The server compacts after each chat, outside its channel lock.
		if err := h.Compact(histBase.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if h.Size() > limit {
			t.Fatalf("after %d adds size = %d, over the cap", i+1, h.Size())
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		maxFile = max(maxFile, int(fi.Size()))
		if i == 0 {
			maxLine = h.Size()
		}
	}
	if maxLine > 4200 {
		t.Fatalf("line is %d bytes: html was escaped", maxLine)
	}
	if maxFile > 2*limit+maxLine {
		t.Fatalf("file grew to %d bytes, cap is %d", maxFile, limit)
	}
	got, _ := h.Page("c", "", time.Time{}, 5000)
	if len(got) == 0 || got[len(got)-1].ID != hmsg("", n-1, time.Time{}).ID {
		t.Fatal("newest line not kept")
	}
	// What is on disk reloads to the same newest line.
	h2 := openH(t, p)
	if got, _ := h2.Page("c", "", time.Time{}, 1); len(got) != 1 || got[0].ID != hmsg("", n-1, time.Time{}).ID {
		t.Fatal("newest line not in the file")
	}
}

func TestTrimZeroDaysDeletesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 8)
	h.Add(hmsg("c", 1, histBase))
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	h.SetLimits(0, 8)
	if err := h.Trim(histBase); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if got, more := h.Page("c", "", time.Time{}, 10); len(got) != 0 || more {
		t.Fatalf("got %v", got)
	}
	if err := h.Add(hmsg("c", 2, histBase)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file reappeared: %v", err)
	}
	if h.Size() != 0 {
		t.Fatalf("size %d", h.Size())
	}
}

func TestTrimZeroDaysClearsMemoryWhenRemoveFails(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 1, histBase))
	// A non-empty directory cannot be removed, so os.Remove fails.
	dir := filepath.Join(t.TempDir(), "h.jsonl")
	if err := os.MkdirAll(filepath.Join(dir, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.path = dir
	h.SetLimits(0, 8)
	if err := h.Trim(histBase); err == nil {
		t.Fatal("want remove error")
	}
	if got, more := h.Page("c", "", time.Time{}, 10); len(got) != 0 || more || h.Size() != 0 {
		t.Fatalf("memory not cleared: %v size %d", got, h.Size())
	}
}

func TestAddWriteFailureStartsFreshLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 8)
	if err := h.Add(hmsg("c", 1, histBase)); err != nil {
		t.Fatal(err)
	}
	h.write = func(f *os.File, b []byte) (int, error) {
		n, _ := f.Write(b[:len(b)/2])
		return n, errors.New("disk full")
	}
	if err := h.Add(hmsg("c", 2, histBase.Add(time.Second))); err == nil {
		t.Fatal("want write error")
	}
	h.write = nil
	if err := h.Add(hmsg("c", 3, histBase.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	h2 := openH(t, p)
	got, _ := h2.Page("c", "", time.Time{}, 10)
	if len(got) != 2 || got[0].Text != "msg 1" || got[1].Text != "msg 3" {
		t.Fatalf("got %+v", got)
	}
	if h2.Skipped() != 1 {
		t.Fatalf("skipped %d", h2.Skipped())
	}
}

func TestOpenRemovesStaleTemps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h.jsonl")
	stale := []string{"h.jsonl.tmp123", "h.jsonl.tmp4294967295"}
	keep := []string{"h.jsonl.tmp", "h.jsonl.tmpx1", "other.jsonl.tmp123", "h.jsonl.tmp12.bak"}
	for _, n := range append(append([]string{}, stale...), keep...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	openH(t, p)
	for _, n := range stale {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s still there (err=%v)", n, err)
		}
	}
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s removed: %v", n, err)
		}
	}
}

func TestHistoryNewest(t *testing.T) {
	h := openH(t, "")
	if !h.Newest().IsZero() {
		t.Fatal("empty store has a newest time")
	}
	h.Add(hmsg("a", 1, histBase.Add(5*time.Second)))
	h.Add(hmsg("b", 2, histBase.Add(9*time.Second)))
	h.Add(hmsg("a", 3, histBase.Add(7*time.Second)))
	if got := h.Newest(); !got.Equal(histBase.Add(9 * time.Second)) {
		t.Fatalf("newest %v", got)
	}
}

func TestHistoryMemoryOnly(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 1, histBase.Add(-100*24*time.Hour)))
	h.Add(hmsg("c", 2, histBase))
	if got, _ := h.Page("c", "", time.Time{}, 10); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if err := h.Trim(histBase); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.Page("c", "", time.Time{}, 10); len(got) != 1 {
		t.Fatalf("after trim %d", len(got))
	}
}

func TestHistoryDuplicateAddIgnored(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 1, histBase))
	h.Add(hmsg("c", 1, histBase))
	if got, _ := h.Page("c", "", time.Time{}, 10); len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
}

// Once the store is at its cap an Add evicts in place: its cost must not
// grow with the number of stored lines (a rebuild of the indexes would).
func TestAddAtCapDoesNotRebuild(t *testing.T) {
	allocs := func(stored int) float64 {
		h := openH(t, "")
		h.SetLimits(30, 64)
		for i := 0; i < stored; i++ {
			h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
		}
		h.SetLimits(30, 1)
		h.Trim(histBase.Add(time.Hour)) // down to the cap
		i := stored
		return testing.AllocsPerRun(50, func() {
			i++
			h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
		})
	}
	small, big := allocs(8000), allocs(30000)
	if big > 40 || big > small+10 {
		t.Fatalf("allocs per Add at cap: %.0f with 8000 stored lines, %.0f with 30000", small, big)
	}
}

// A rewrite does not hold the lock while it writes: Add and Page go on, and
// lines added meanwhile survive in the new file.
func TestRewriteDoesNotBlockAdd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 8)
	for i := 0; i < 10; i++ {
		h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
	}
	started, release := make(chan struct{}), make(chan struct{})
	h.tmpWrite = func(f *os.File, b []byte) (int, error) {
		close(started)
		<-release
		return f.Write(b)
	}
	h.SetLimits(1, 8) // days cap: everything before the cutoff goes
	done := make(chan error, 1)
	go func() { done <- h.Trim(histBase.Add(36*time.Hour + 5*time.Second)) }()
	<-started
	during := make(chan struct{})
	go func() {
		defer close(during)
		h.Add(hmsg("c", 50, histBase.Add(36*time.Hour+10*time.Second)))
		h.Page("c", "", time.Time{}, 10)
	}()
	select {
	case <-during:
	case <-time.After(2 * time.Second):
		t.Fatal("Add/Page blocked while the rewrite was writing")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.Close()
	h2 := openH(t, p)
	got, _ := h2.Page("c", "", time.Time{}, 100)
	if len(got) != 1 || got[0].Text != "msg 50" {
		t.Fatalf("after reopen: %+v", got)
	}
	if h.Size() != h2.Size() {
		t.Fatalf("size %d, reopened %d", h.Size(), h2.Size())
	}
}

// A rename that keeps failing must not be retried on every chat.
func TestCompactBacksOffAfterFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	h := openH(t, p)
	h.SetLimits(30, 64)
	pad := strings.Repeat("x", 1000)
	for i := 0; i < 2500; i++ {
		m := hmsg("c", i, histBase.Add(time.Duration(i)*time.Second))
		m.Text = pad
		h.Add(m)
	}
	h.SetLimits(30, 1) // file is now over twice the cap
	renames := 0
	h.rename = func(string, string) error { renames++; return errors.New("locked") }
	now := histBase.Add(time.Hour)
	if err := h.Compact(now); err == nil {
		t.Fatal("want the rename error")
	}
	if err := h.Compact(now.Add(30 * time.Second)); err != nil || renames != 1 {
		t.Fatalf("inside the backoff: err=%v renames=%d", err, renames)
	}
	if err := h.Compact(now.Add(2 * time.Minute)); err == nil || renames != 2 {
		t.Fatalf("after the backoff: err=%v renames=%d", err, renames)
	}
}

// A rewrite re-encodes lines without HTML escapes; the size follows.
func TestRewriteUpdatesSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.jsonl")
	m := hmsg("c", 1, histBase)
	m.Text = "<<<<<<<<<<"
	os.WriteFile(p, []byte(line(t, m)+"garbage\n"), 0o600) // old escaped line, one bad line
	h := openH(t, p)
	if err := h.Trim(histBase); err != nil { // the bad line forces a rewrite
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if h.Size() != int(fi.Size()) {
		t.Fatalf("size %d, file %d", h.Size(), fi.Size())
	}
}

func TestNewestBefore(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("a", 1, histBase))
	h.Add(hmsg("b", 2, histBase.Add(time.Hour)))
	h.Add(hmsg("a", 3, histBase.Add(48*time.Hour)))
	if got := h.NewestBefore(histBase.Add(2 * time.Hour)); !got.Equal(histBase.Add(time.Hour)) {
		t.Fatalf("got %v", got)
	}
	if got := h.NewestBefore(histBase.Add(-time.Second)); !got.IsZero() {
		t.Fatalf("got %v", got)
	}
}

func TestAddAfterCloseFails(t *testing.T) {
	for _, p := range []string{"", filepath.Join(t.TempDir(), "h.jsonl")} {
		h := openH(t, p)
		h.Add(hmsg("c", 1, histBase))
		h.Close()
		if err := h.Add(hmsg("c", 2, histBase.Add(time.Second))); err == nil {
			t.Fatalf("path %q: Add after Close succeeded", p)
		}
		if got, _ := h.Page("c", "", time.Time{}, 10); len(got) != 1 {
			t.Fatalf("path %q: page %v", p, got)
		}
	}
}
