package discover

// WithLANDestsForTest overrides the LAN broadcast destinations and binds the
// probe sockets to loopback so tests never open a wildcard socket. Every
// Probe call in this package's tests goes through it.
func (o ProbeOpts) WithLANDestsForTest(d ...string) ProbeOpts {
	o.ListenAddr = "127.0.0.1:0"
	o.lanDests = append([]string{}, d...)
	return o
}
