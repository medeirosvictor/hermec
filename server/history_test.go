package server

import (
	"encoding/json"
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
	got, more := h2.Page("general", "", 10)
	if more || len(got) != 2 || got[0].ID != hmsg("", 1, time.Time{}).ID || got[1].ID != hmsg("", 2, time.Time{}).ID {
		t.Fatalf("got %+v more=%v", got, more)
	}
	if r, _ := h2.Page("random", "", 10); len(r) != 1 {
		t.Fatalf("random: %+v", r)
	}
}

func TestHistoryPaging(t *testing.T) {
	h := openH(t, filepath.Join(t.TempDir(), "h.jsonl"))
	for i := 0; i < 25; i++ {
		h.Add(hmsg("c", i, histBase.Add(time.Duration(i)*time.Second)))
	}
	p1, more := h.Page("c", "", 10)
	if !more || len(p1) != 10 || p1[0].Text != "msg 15" || p1[9].Text != "msg 24" {
		t.Fatalf("p1 %v more=%v", p1, more)
	}
	p2, more := h.Page("c", p1[0].ID, 10)
	if !more || len(p2) != 10 || p2[0].Text != "msg 5" || p2[9].Text != "msg 14" {
		t.Fatalf("p2 %v more=%v", p2, more)
	}
	p3, more := h.Page("c", p2[0].ID, 10)
	if more || len(p3) != 5 || p3[0].Text != "msg 0" {
		t.Fatalf("p3 %v more=%v", p3, more)
	}
	if m, more := h.Page("c", "nope", 10); m != nil || more {
		t.Fatalf("unknown: %v %v", m, more)
	}
}

func TestHistoryOrderTiesByID(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 9, histBase))
	h.Add(hmsg("c", 3, histBase))
	got, _ := h.Page("c", "", 10)
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
	if got, _ := h.Page("c", "", 10); len(got) != 2 {
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
	got, _ := h.Page("c", "", 10)
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
	got, _ := h.Page("c", "", 10)
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
	h.SetLimits(30, 1)
	pad := strings.Repeat("x", 1000)
	n := 1500
	for i := 0; i < n; i++ {
		m := hmsg([]string{"a", "b"}[i%2], i, histBase.Add(time.Duration(i)*time.Second))
		m.Text = pad
		h.Add(m)
	}
	if h.Size() <= 1<<20 {
		t.Fatalf("setup too small: %d", h.Size())
	}
	if err := h.Trim(histBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if h.Size() > 1<<20 {
		t.Fatalf("size %d", h.Size())
	}
	a, _ := h.Page("a", "", 5000)
	b, _ := h.Page("b", "", 5000)
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
	if got, more := h.Page("c", "", 10); len(got) != 0 || more {
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

func TestHistoryMemoryOnly(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 1, histBase.Add(-100*24*time.Hour)))
	h.Add(hmsg("c", 2, histBase))
	if got, _ := h.Page("c", "", 10); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if err := h.Trim(histBase); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.Page("c", "", 10); len(got) != 1 {
		t.Fatalf("after trim %d", len(got))
	}
}

func TestHistoryDuplicateAddIgnored(t *testing.T) {
	h := openH(t, "")
	h.Add(hmsg("c", 1, histBase))
	h.Add(hmsg("c", 1, histBase))
	if got, _ := h.Page("c", "", 10); len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
}
