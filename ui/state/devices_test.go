package state

import (
	"path/filepath"
	"testing"
)

func TestResolveDevice(t *testing.T) {
	avail := []string{"Headset (USB)", "Micrófono 日本"}
	cases := []struct {
		want, use string
		notice    bool
	}{
		{"", "", false},
		{"Headset (USB)", "Headset (USB)", false},
		{"Micrófono 日本", "Micrófono 日本", false},
		{"Unplugged", "", true},
	}
	for _, c := range cases {
		use, n := ResolveDevice("input", c.want, avail)
		if use != c.use || (n != "") != c.notice {
			t.Errorf("%q: got %q %q", c.want, use, n)
		}
	}
	if use, n := ResolveDevice("output", "x", nil); use != "" || n == "" {
		t.Errorf("empty list: %q %q", use, n)
	}
}

func TestNextDevice(t *testing.T) {
	avail := []string{"a", "b"}
	seq := []string{"a", "b", "", "a"}
	cur := ""
	for i, want := range seq {
		cur = NextDevice(cur, avail)
		if cur != want {
			t.Fatalf("step %d: got %q want %q", i, cur, want)
		}
	}
	if got := NextDevice("gone", avail); got != "a" {
		t.Errorf("unknown cur: %q", got)
	}
	if got := NextDevice("", nil); got != "" {
		t.Errorf("no devices: %q", got)
	}
}

func TestSettingsDeviceRoundTripUnicode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	in := Settings{InputDevice: "Micrófono (Realtek® 高清)", OutputDevice: `Altavoces "USB" \ Ж`}
	if err := SaveSettings(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSettings(p)
	if err != nil || out.InputDevice != in.InputDevice || out.OutputDevice != in.OutputDevice {
		t.Fatalf("got %+v, %v", out, err)
	}
}
