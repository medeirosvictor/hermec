package embeddedprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/netpath"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
)

type loopNetwork struct{ pion transport.Net }

func newLoopNetwork(t *testing.T) *loopNetwork {
	t.Helper()
	p, err := stdnet.NewNet()
	if err != nil {
		t.Fatal(err)
	}
	return &loopNetwork{pion: p}
}
func (n *loopNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
func (n *loopNetwork) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, network, address)
}
func (n *loopNetwork) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return (&net.ListenConfig{}).ListenPacket(ctx, network, address)
}
func (n *loopNetwork) PionNet() transport.Net { return n.pion }

type blockedPionNet struct{ transport.Net }

func (blockedPionNet) ListenPacket(string, string) (net.PacketConn, error) {
	return nil, errors.New("probe media blocked")
}
func (blockedPionNet) ListenUDP(string, *net.UDPAddr) (transport.UDPConn, error) {
	return nil, errors.New("probe media blocked")
}

func TestScenarioStandbyIsNotReadyFromSignalingOnly(t *testing.T) {
	primaryPort, standbyPort := scenarioPorts(t)
	a, b, c := newLoopNetwork(t), newLoopNetwork(t), newLoopNetwork(t)
	c.pion = blockedPionNet{Net: c.pion}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := RunScenario(ctx, ScenarioConfig{Networks: [3]netpath.Network{a, b, c}, PrimaryAddr: "127.0.0.1", StandbyAddr: "127.0.0.1", Port: primaryPort, StandbyPort: standbyPort, LoopbackICE: true, Timeout: 25 * time.Second})
	if err == nil {
		t.Fatal("scenario passed with standby media blocked")
	}
	if !report.Checks["standby_signaling"].Passed {
		t.Fatalf("standby signaling did not pass: %+v", report.Checks)
	}
	if report.Checks["standby_media_ready"].Passed {
		t.Fatalf("signaling-only standby marked ready: %+v", report.Checks)
	}
}

func TestScenarioRequiresMediaBothWays(t *testing.T) {
	if testing.Short() {
		t.Skip("full media and failover scenario")
	}
	primaryPort, standbyPort := scenarioPorts(t)
	a, b, c := newLoopNetwork(t), newLoopNetwork(t), newLoopNetwork(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	report, err := RunScenario(ctx, ScenarioConfig{Networks: [3]netpath.Network{a, b, c}, PrimaryAddr: "127.0.0.1", StandbyAddr: "127.0.0.1", Port: primaryPort, StandbyPort: standbyPort, LoopbackICE: true, Timeout: 70 * time.Second})
	if err != nil {
		t.Fatalf("RunScenario: %v\nreport: %+v", err, report)
	}
	for _, name := range []string{"primary_active", "standby_signaling", "standby_media_ready", "bidirectional_audio_camera_screen", "takeover_retains_call_media"} {
		if !report.Checks[name].Passed {
			t.Fatalf("check %q failed: %+v", name, report.Checks[name])
		}
	}
}

func TestScenarioCancellationReleasesListeners(t *testing.T) {
	primaryPort, standbyPort := scenarioPorts(t)
	a, b, c := newLoopNetwork(t), newLoopNetwork(t), newLoopNetwork(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = RunScenario(ctx, ScenarioConfig{Networks: [3]netpath.Network{a, b, c}, PrimaryAddr: "127.0.0.1", StandbyAddr: "127.0.0.1", Port: primaryPort, StandbyPort: standbyPort, LoopbackICE: true, Timeout: time.Second})
	for _, port := range []int{primaryPort, standbyPort} {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		if err != nil {
			t.Fatalf("TCP port %d leaked: %v", port, err)
		}
		ln.Close()
		pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		if err != nil {
			t.Fatalf("UDP port %d leaked: %v", port, err)
		}
		pc.Close()
	}
}

func scenarioPorts(t *testing.T) (int, int) {
	t.Helper()
	pick := func() int {
		for {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := ln.Addr().(*net.TCPAddr).Port
			pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
			if err == nil {
				pc.Close()
				ln.Close()
				return port
			}
			ln.Close()
		}
	}
	return pick(), pick()
}
