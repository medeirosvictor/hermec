package client

// SetScreenGenForTest makes gen the screen subscription gen in force, so other
// packages can stage a late refusal for it without a server.
func (c *Client) SetScreenGenForTest(gen uint32) {
	c.screenMu.Lock()
	c.screenWire = gen
	c.screenMu.Unlock()
}
