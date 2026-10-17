package discover

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
)

// SelfAddrs returns this machine's own Tailscale IPv4 (in 100.64.0.0/10) and
// first private LAN IPv4. Either may be "".
func SelfAddrs(ctx context.Context) (tailscaleIP, lanIP string) {
	return selfTailscaleIP(ctx), selfLANIP()
}

func selfTailscaleIP(parent context.Context) string {
	bin, err := lookPath("tailscale")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, statusTimeout)
	defer cancel()
	out, err := runStatus(ctx, bin)
	if err != nil {
		return ""
	}
	var st struct {
		Self struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	for _, s := range st.Self.TailscaleIPs {
		if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() && cgnat.Contains(ip) {
			return s
		}
	}
	return ""
}

// dialUDP is the default-route probe seam; a UDP "dial" sends no packets.
var dialUDP = func() (net.Addr, error) {
	c, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.LocalAddr(), nil
}

// routeLANIP is the source address of the default route when it is a private
// non-Tailscale IPv4; adapters like WSL, Hyper-V or Docker never win this way.
func routeLANIP() string {
	a, err := dialUDP()
	if err != nil {
		return ""
	}
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return ""
	}
	ip, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return ""
	}
	ip = ip.Unmap()
	if ip.Is4() && ip.IsPrivate() && !cgnat.Contains(ip) {
		return ip.String()
	}
	return ""
}

func selfLANIP() string {
	if ip := routeLANIP(); ip != "" {
		return ip
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if ip.Is4() && ip.IsPrivate() && !cgnat.Contains(ip) {
				return ip.String()
			}
		}
	}
	return ""
}
