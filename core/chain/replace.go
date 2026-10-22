package chain

import (
	"errors"
	"fmt"
)

// Replace swaps the chain's contents for es, a full chain (genesis first) that
// must verify and share this chain's ID. A chain with a path is rewritten
// whole; if that fails the old contents stay. It is how a host that lost a
// split-brain takes on the winner's chain, in place so every holder of c
// sees it.
func (c *Chain) Replace(es []Entry) error {
	n, err := FromEntries(es)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n.state.ID != c.state.ID {
		return fmt.Errorf("%w: different chain", ErrBadChain)
	}
	if len(n.entries) < len(c.entries) {
		prefix := true
		for i, e := range n.entries {
			prefix = prefix && e.Hash() == c.entries[i].Hash()
		}
		if prefix {
			return ErrRollback
		}
	}
	oldEntries, oldSize, oldState := c.entries, c.size, c.state
	c.entries, c.size, c.state = n.entries, n.size, n.state
	if c.path != "" {
		if err := c.setPathLocked(c.path); err != nil {
			c.entries, c.size, c.state = oldEntries, oldSize, oldState
			return err
		}
	}
	return nil
}

// ErrRollback means a replacement chain is a strict prefix of the current one,
// so taking it would drop entries without any fork to justify it.
var ErrRollback = errors.New("chain: replacement would roll the chain back")
