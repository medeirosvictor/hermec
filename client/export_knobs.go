package client

// SetWatchGenForTest makes gen the screen subscription gen in force, so other
// packages can stage a late refusal for it without a server.
func (c *Client) SetWatchGenForTest(gen uint32) {
	c.watchMu.Lock()
	c.watchWire = gen
	c.watchMu.Unlock()
}
