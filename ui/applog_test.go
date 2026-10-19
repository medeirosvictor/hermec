package ui

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestLogfAlwaysWritesFile(t *testing.T) {
	var buf bytes.Buffer
	g := &game{}
	g.setLogWriter(&buf)
	g.logf("joining %s", "general")
	if !strings.Contains(buf.String(), "joining general") {
		t.Fatalf("file missing breadcrumb: %q", buf.String())
	}
	if strings.Count(buf.String(), "joining general") != 1 {
		t.Fatal("line must appear exactly once")
	}
}

func TestLogfNoWriterIsSafe(t *testing.T) {
	g := &game{}
	g.logf("nothing %d", 1)
}

func TestHostedServerLinesPrefixed(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) })
	var cfg server.Config
	serverLogf(&cfg)
	if cfg.Logf == nil {
		t.Fatal("Logf not set")
	}
	cfg.Logf("voice %q: hello", "v")
	if got := buf.String(); !strings.HasPrefix(got, "[server] voice") {
		t.Fatalf("got %q", got)
	}
}

func TestHealthLineFormat(t *testing.T) {
	got := healthLine(42*time.Millisecond, true, []state.Participant{
		{Name: "ana", Jitter: 7 * time.Millisecond, Concealed: 0.025, OK: true},
		{Name: "bo", OK: false},
	})
	want := "health: ping 42 ms; ana jitter 7 ms concealed 2.5%; bo no data"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := healthLine(0, false, nil); got != "health: ping no data" {
		t.Fatalf("got %q", got)
	}
}
