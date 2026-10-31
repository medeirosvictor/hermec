package state

import "testing"

func TestValidateCreate(t *testing.T) {
	cases := []struct {
		name, port string
		wantName   string
		wantPort   int
		wantErr    string
	}{
		{"Den", "7697", "Den", 7697, ""},
		{"  Den ", "1", "Den", 1, ""},
		{"Den", "65535", "Den", 65535, ""},
		{"", "7697", "", 0, NameRequiredMsg},
		{"   ", "7697", "", 0, NameRequiredMsg},
		{"Den", "", "", 0, PortRangeMsg},
		{"Den", "0", "", 0, PortRangeMsg},
		{"Den", "70000", "", 0, PortRangeMsg},
		{"Den", "8x", "", 0, PortRangeMsg},
	}
	for _, c := range cases {
		n, p, e := ValidateCreate(c.name, c.port)
		if n != c.wantName || p != c.wantPort || e != c.wantErr {
			t.Errorf("ValidateCreate(%q, %q) = %q, %d, %q", c.name, c.port, n, p, e)
		}
	}
}
