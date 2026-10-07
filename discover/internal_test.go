package discover

import (
	"net"
	"testing"
)

func TestDirectedBroadcast(t *testing.T) {
	cases := []struct{ cidr, want string }{
		{"192.168.1.37/24", "192.168.1.255"},
		{"10.1.17.5/20", "10.1.31.255"},
		{"10.0.0.9/32", "10.0.0.9"},
		{"fe80::1/64", ""},
	}
	for _, c := range cases {
		ip, n, err := net.ParseCIDR(c.cidr)
		if err != nil {
			t.Fatal(err)
		}
		got := directedBroadcast(&net.IPNet{IP: ip, Mask: n.Mask})
		if c.want == "" {
			if got != nil {
				t.Errorf("%s: got %v, want nil", c.cidr, got)
			}
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s: got %v, want %s", c.cidr, got, c.want)
		}
	}
}

func TestBroadcastDestsIncludesLimited(t *testing.T) {
	d := broadcastDests(4567)
	if len(d) == 0 || d[0] != "255.255.255.255:4567" {
		t.Fatalf("got %v", d)
	}
	for _, x := range d {
		if _, _, err := net.SplitHostPort(x); err != nil {
			t.Errorf("bad dest %q", x)
		}
	}
}
