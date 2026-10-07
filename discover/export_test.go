package discover

// WithLANDestsForTest overrides the LAN broadcast destinations.
func (o ProbeOpts) WithLANDestsForTest(d ...string) ProbeOpts {
	o.lanDests = append([]string{}, d...)
	return o
}
