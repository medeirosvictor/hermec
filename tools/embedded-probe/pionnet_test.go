package embeddedprobe

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestPionUDPRoundTripDeadlinesAndClose(t *testing.T) {
	packets := newMemoryPacketNetwork()
	a := openFake(t, t.TempDir(), &fakeBackend{packets: packets, state: profileState{
		Local: mustAddrs("100.64.2.1"), Peers: mustAddrs("100.64.2.2"),
	}})
	b := openFake(t, t.TempDir(), &fakeBackend{packets: packets, state: profileState{
		Local: mustAddrs("100.64.2.2"), Peers: mustAddrs("100.64.2.1"),
	}})
	ac, err := a.pion.ListenUDP("udp4", &net.UDPAddr{Port: 9001})
	if err != nil {
		t.Fatal(err)
	}
	defer ac.Close()
	bc, err := b.pion.ListenUDP("udp4", &net.UDPAddr{Port: 9002})
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	if got, ok := ac.LocalAddr().(*net.UDPAddr); !ok || !got.IP.Equal(net.ParseIP("100.64.2.1")) {
		t.Fatalf("A LocalAddr = %T %v", ac.LocalAddr(), ac.LocalAddr())
	}
	if got, ok := bc.LocalAddr().(*net.UDPAddr); !ok || !got.IP.Equal(net.ParseIP("100.64.2.2")) {
		t.Fatalf("B LocalAddr = %T %v", bc.LocalAddr(), bc.LocalAddr())
	}
	if _, err := ac.WriteToUDP([]byte("a-to-b"), bc.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, from, err := bc.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "a-to-b" || from.String() != ac.LocalAddr().String() {
		t.Fatalf("B read %q from %v", buf[:n], from)
	}
	if err := ac.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ac.ReadFromUDP(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	if err := ac.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := ac.ReadFromUDP(buf); done <- err }()
	time.Sleep(10 * time.Millisecond)
	if err := ac.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked read error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock ReadFromUDP")
	}
}

func TestPionInterfacesContainOnlyProfileAddresses(t *testing.T) {
	n := openFake(t, t.TempDir(), &fakeBackend{state: profileState{Local: mustAddrs("100.64.3.1", "fd7a:115c:a1e0::1")}})
	ifcs, err := n.pion.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(ifcs) != 1 || ifcs[0].Name != "hermec-private" {
		t.Fatalf("interfaces = %+v", ifcs)
	}
	addrs, err := ifcs[0].Addrs()
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 2 {
		t.Fatalf("addresses = %v", addrs)
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || (!ip.Equal(net.ParseIP("100.64.3.1")) && !ip.Equal(net.ParseIP("fd7a:115c:a1e0::1"))) {
			t.Fatalf("unexpected profile address %v", addr)
		}
	}
}

func mustAddrs(raw ...string) []netip.Addr {
	out := make([]netip.Addr, len(raw))
	for i, s := range raw {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}
