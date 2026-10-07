package discover_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/server"
)

func startServer(t *testing.T, name string) (addr string, port int) {
	t.Helper()
	cfg := server.DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	cfg.ServerName = name
	s := server.New(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	_, p, _ := net.SplitHostPort(s.Addr())
	port, _ = strconv.Atoi(p)
	return s.Addr(), port
}

func noPeers() []string { return nil }

func TestProbeSavedFindsServer(t *testing.T) {
	addr, port := startServer(t, "Lab")
	got := discover.Probe(context.Background(), discover.ProbeOpts{
		Port: port, ExtraHosts: []string{addr}, TailscalePeers: noPeers, Budget: 600 * time.Millisecond,
	}.WithLANDestsForTest())
	if len(got) != 1 || got[0].Addr != addr || got[0].Source != "saved" || got[0].Name != "Lab" {
		t.Fatalf("got %+v", got)
	}
}

func TestProbeSavedHostWithoutPortUsesOptsPort(t *testing.T) {
	addr, port := startServer(t, "Lab")
	got := discover.Probe(context.Background(), discover.ProbeOpts{
		Port: port, ExtraHosts: []string{"127.0.0.1"}, TailscalePeers: noPeers, Budget: 600 * time.Millisecond,
	}.WithLANDestsForTest())
	if len(got) != 1 || got[0].Addr != addr {
		t.Fatalf("got %+v", got)
	}
}

func TestProbeTailnetSeam(t *testing.T) {
	addr, port := startServer(t, "Lab")
	got := discover.Probe(context.Background(), discover.ProbeOpts{
		Port: port, TailscalePeers: func() []string { return []string{"127.0.0.1"} }, Budget: 600 * time.Millisecond,
	}.WithLANDestsForTest())
	if len(got) != 1 || got[0].Addr != addr || got[0].Source != "tailnet" {
		t.Fatalf("got %+v", got)
	}
}

func TestProbeDedupePrefersLan(t *testing.T) {
	addr, port := startServer(t, "Lab")
	got := discover.Probe(context.Background(), discover.ProbeOpts{
		Port: port, ExtraHosts: []string{addr}, TailscalePeers: func() []string { return []string{"127.0.0.1"} },
		Budget: 600 * time.Millisecond,
	}.WithLANDestsForTest(addr))
	if len(got) != 1 || got[0].Source != "lan" {
		t.Fatalf("got %+v", got)
	}
}

func TestProbeNothingFound(t *testing.T) {
	got := discover.Probe(context.Background(), discover.ProbeOpts{
		Port: 1, ExtraHosts: []string{"127.0.0.1:1", "bad host::"}, TailscalePeers: noPeers, Budget: 300 * time.Millisecond,
	}.WithLANDestsForTest())
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want empty non-nil", got)
	}
}

func TestProbeCtxCancelReturnsEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	discover.Probe(ctx, discover.ProbeOpts{Port: 1, TailscalePeers: noPeers, Budget: 10 * time.Second}.WithLANDestsForTest())
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}
