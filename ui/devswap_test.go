package ui

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

type fakeSwapper struct {
	io.Closer
	calls []string
	err   error
}

func (f *fakeSwapper) SwapDevice(name string) error {
	f.calls = append(f.calls, name)
	return f.err
}

func swapGame(t *testing.T) (g *game, src, sink *fakeSwapper, s *session) {
	t.Helper()
	g = newTestGame(t)
	g.settingsPath = filepath.Join(t.TempDir(), "settings.toml")
	g.swapCh = make(chan devSwapResult, 2)
	s = newSession("k", "ws://x", "me")
	g.sess[s.key] = s
	g.callKey = s.key
	src, sink = &fakeSwapper{}, &fakeSwapper{}
	g.vc = voiceCall{src: src, sink: sink, inDev: "old-mic", outDev: "old-spk"}
	return g, src, sink, s
}

func settleSwap(g *game) {
	g.applyDevSwap(<-g.swapCh)
}

func TestSwapDevicesMovesOnlyWhatChanged(t *testing.T) {
	g, src, sink, s := swapGame(t)
	g.set.InputDevice, g.set.OutputDevice = "usb-mic", "old-spk"
	g.swapDevices()
	settleSwap(g)
	if len(src.calls) != 1 || src.calls[0] != "usb-mic" || len(sink.calls) != 0 {
		t.Fatalf("src %v, sink %v", src.calls, sink.calls)
	}
	if g.vc.inDev != "usb-mic" || g.vc.swapping {
		t.Fatalf("vc = %+v", g.vc)
	}
	if s.st.Status != "microphone switched" {
		t.Fatalf("status = %q", s.st.Status)
	}
}

func TestSwapDevicesFailureKeepsOldAndReverts(t *testing.T) {
	g, src, _, s := swapGame(t)
	src.err = errors.New("device busy")
	g.set.InputDevice = "usb-mic"
	g.swapDevices()
	settleSwap(g)
	if g.vc.inDev != "old-mic" || g.set.InputDevice != "old-mic" {
		t.Fatalf("inDev %q, setting %q", g.vc.inDev, g.set.InputDevice)
	}
	if s.st.Status != "couldn't switch microphone; keeping the previous one" {
		t.Fatalf("status = %q", s.st.Status)
	}
}

func TestSwapDevicesNoopWithoutCallOrChange(t *testing.T) {
	g, src, _, _ := swapGame(t)
	g.swapDevices() // settings equal the active devices
	if g.vc.swapping || len(src.calls) != 0 {
		t.Fatal("swapped with nothing to change")
	}
	g.vc = voiceCall{}
	g.set.InputDevice = "usb-mic"
	g.swapDevices() // no call
	if g.vc.swapping {
		t.Fatal("swapped with no call")
	}
}

func TestSwapDevicesQueuesAChangeDuringASwap(t *testing.T) {
	g, src, _, _ := swapGame(t)
	g.set.InputDevice = "a"
	g.swapDevices()
	g.set.InputDevice = "b"
	g.swapDevices() // in flight: remembered, not started
	if !g.vc.swapAgain {
		t.Fatalf("swapAgain = %v", g.vc.swapAgain)
	}
	settleSwap(g) // finishes "a", then starts "b"
	settleSwap(g)
	if want := []string{"a", "b"}; len(src.calls) != 2 || src.calls[0] != want[0] || src.calls[1] != want[1] {
		t.Fatalf("calls = %v", src.calls)
	}
	if g.vc.inDev != "b" {
		t.Fatalf("inDev = %q", g.vc.inDev)
	}
}

func TestSwapResultForAnEndedCallIsIgnored(t *testing.T) {
	g, _, _, _ := swapGame(t)
	g.set.InputDevice = "usb-mic"
	g.swapDevices()
	r := <-g.swapCh
	g.vc = voiceCall{src: &fakeSwapper{}, sink: &fakeSwapper{}, inDev: "x"} // a different call
	g.applyDevSwap(r)
	if g.vc.inDev != "x" {
		t.Fatalf("stale result applied: %+v", g.vc)
	}
}
