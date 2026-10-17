package ui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"

	"github.com/medeirosvictor/hermec/server"
)

func TestHostedConfigRoundTrip(t *testing.T) {
	cfg := hostedConfig("  My‮Server ", "pw", 7697, "FP")
	path := filepath.Join(t.TempDir(), "sub", "hosted.toml")
	if err := saveHosted(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadHosted(path)
	if err != nil || !ok {
		t.Fatalf("loadHosted ok=%v err=%v", ok, err)
	}
	if got.ServerName != "MyServer" || got.Addr != ":7697" || got.Password != "pw" {
		t.Errorf("got name=%q addr=%q pw=%q", got.ServerName, got.Addr, got.Password)
	}
	if !slices.Equal(got.Channels, []string{"general"}) || !slices.Equal(got.VoiceChannels, []string{"voice"}) {
		t.Errorf("channels=%v voice=%v", got.Channels, got.VoiceChannels)
	}
	if !slices.Contains(got.Roles.RolesFor("FP"), "admin") {
		t.Errorf("FP roles = %v", got.Roles.RolesFor("FP"))
	}
	if r := got.Roles.RolesFor("other"); !slices.Equal(r, []string{"user"}) {
		t.Errorf("other roles = %v", r)
	}
	if hostedPort(got) != 7697 {
		t.Errorf("hostedPort = %d", hostedPort(got))
	}
	if runtime.GOOS == "windows" {
		return // file modes are not meaningful on Windows
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestLoadHostedMissing(t *testing.T) {
	_, ok, err := loadHosted(filepath.Join(t.TempDir(), "nope.toml"))
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want false, nil", ok, err)
	}
}

func TestIsPortInUse(t *testing.T) {
	wsa := fmt.Errorf("server: listen: %w", &net.OpError{Err: os.NewSyscallError("bind", syscall.Errno(10048))})
	if !isPortInUse(wsa) {
		t.Error("WSAEADDRINUSE not detected")
	}
	if !isPortInUse(fmt.Errorf("x: %w", server.ErrDiscoveryBind)) {
		t.Error("ErrDiscoveryBind not detected")
	}
	if isPortInUse(errors.New("boom")) {
		t.Error("plain error detected")
	}
}

func TestIsPortInUseReal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s := server.New(server.Config{Addr: ln.Addr().String(), Discoverable: false})
	err = s.Start()
	if err == nil {
		t.Fatal("Start succeeded on a busy port")
	}
	if !isPortInUse(err) {
		t.Errorf("isPortInUse(%v) = false", err)
	}
}

func TestPortInUseMsg(t *testing.T) {
	if got, want := portInUseMsg(7697), "port 7697 is in use (is hermec-server running?)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInviteAddress(t *testing.T) {
	for _, c := range []struct{ ts, lan, want string }{
		{"100.64.1.2", "192.168.2.10", "ws://100.64.1.2:7697/"},
		{"", "192.168.2.10", "ws://192.168.2.10:7697/"},
		{"", "", "no network address found"},
	} {
		if got := inviteAddress(c.ts, c.lan, 7697); got != c.want {
			t.Errorf("inviteAddress(%q,%q) = %q, want %q", c.ts, c.lan, got, c.want)
		}
	}
}
