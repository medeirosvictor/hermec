package chain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Chain caps; variables so tests can lower them.
var (
	maxEntries = 2048
	maxBytes   = 512 << 10
)

// Errors returned by Chain.
var (
	// ErrConflict means the entry is not the successor of the current head.
	ErrConflict = errors.New("chain: entry does not extend the head")
	// ErrFull means the chain has reached its entry or size cap.
	ErrFull = errors.New("chain: full")
	// ErrBadChain means a signature, link or rule check failed.
	ErrBadChain = errors.New("chain: invalid chain")
)

// Chain is an append-only, verified sequence of entries, optionally persisted
// as one JSON line per entry.
type Chain struct {
	mu      sync.Mutex
	entries []Entry
	size    int // sum of encoded line lengths, newline included
	state   State
	path    string

	persistErr error // sticky: set when a failed append could not be rolled back
	tornTail   int   // bytes of a torn final line Load truncated
}

func marshalLine(e Entry) ([]byte, error) {
	j, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(j, '\n'), nil
}

func linkOK(prev, e Entry) bool {
	h := prev.Hash()
	return e.Seq == prev.Seq+1 && bytes.Equal(e.Prev, h[:])
}

// New starts a chain from a genesis entry.
func New(genesis Entry) (*Chain, error) { return FromEntries([]Entry{genesis}) }

// FromEntries fully verifies es (signatures, links, rules, caps) and builds a
// chain from them.
func FromEntries(es []Entry) (*Chain, error) {
	if len(es) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrBadChain)
	}
	c := &Chain{}
	for i, e := range es {
		if i == 0 {
			if e.Op != OpGenesis || e.Seq != 0 || len(e.Prev) != 0 {
				return nil, fmt.Errorf("%w: entry 0 is not genesis", ErrBadChain)
			}
		}
		if err := c.add(e); err != nil {
			if errors.Is(err, ErrFull) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: entry %d: %w", ErrBadChain, i, err)
		}
	}
	return c, nil
}

// add verifies e against the current head and state and adds it in memory.
// Callers hold the lock (or own c exclusively). It returns the encoded line.
func (c *Chain) add(e Entry) error {
	if n := len(c.entries); n > 0 && !linkOK(c.entries[n-1], e) {
		return ErrConflict
	}
	if !e.VerifySig() {
		return errors.New("bad signature")
	}
	line, err := marshalLine(e)
	if err != nil {
		return err
	}
	if len(c.entries)+1 > maxEntries || c.size+len(line) > maxBytes {
		return ErrFull
	}
	st := c.state.clone()
	if err := st.Apply(e); err != nil {
		return err
	}
	c.entries = append(c.entries, cloneEntry(e))
	c.size += len(line)
	c.state = st
	return nil
}

func cloneEntry(e Entry) Entry {
	e.Prev = append([]byte(nil), e.Prev...)
	if len(e.Prev) == 0 {
		e.Prev = nil
	}
	e.Author = append([]byte(nil), e.Author...)
	e.Body = append([]byte(nil), e.Body...)
	e.Sig = append([]byte(nil), e.Sig...)
	return e
}

// Load reads a chain file and fully verifies it.
//
// A crash in the middle of an append can leave a torn final line (partial,
// NUL-filled, or missing its newline). Load drops that line, truncates the file
// back to the last good line and records how many bytes it removed (see
// TornTail) so the caller can report it. A bad line anywhere before the last
// is ErrBadChain.
func Load(path string) (*Chain, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > int64(maxBytes)+4096 {
		return nil, ErrFull
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var es []Entry
	total, pos, goodEnd := 0, 0, 0
	for pos < len(data) {
		nl := bytes.IndexByte(data[pos:], '\n')
		if nl < 0 {
			break // no trailing newline: torn
		}
		line := data[pos : pos+nl]
		next := pos + nl + 1
		pos = next
		if len(bytes.TrimSpace(line)) == 0 {
			goodEnd = next
			continue
		}
		total += len(line) + 1
		if total > maxBytes || len(es) >= maxEntries {
			return nil, ErrFull
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			if next >= len(data) {
				break // unparseable final line: torn
			}
			return nil, fmt.Errorf("%w: line %d: %v", ErrBadChain, len(es)+1, err)
		}
		es = append(es, e)
		goodEnd = next
	}
	c, err := FromEntries(es)
	if err != nil {
		return nil, err
	}
	if goodEnd < len(data) {
		if err := os.Truncate(path, int64(goodEnd)); err != nil {
			return nil, fmt.Errorf("chain: truncate torn tail: %w", err)
		}
		c.tornTail = len(data) - goodEnd
	}
	c.path = path
	return c, nil
}

// TornTail reports how many bytes of a torn final line Load removed from the
// file; zero when the file was intact.
func (c *Chain) TornTail() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tornTail
}

// Append verifies e as the next entry and, when a path is set, persists it as
// one JSON line before it becomes visible.
func (c *Chain) Append(e Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.persistErr != nil {
		return fmt.Errorf("chain: file may be corrupt after failed rollback: %w", c.persistErr)
	}
	// Verify on a scratch copy of the bookkeeping so persistence failure
	// leaves the chain untouched.
	trial := &Chain{entries: c.entries, size: c.size, state: c.state}
	if err := trial.add(e); err != nil {
		return err
	}
	if c.path != "" {
		line, _ := marshalLine(e)
		if torn, err := appendLine(c.path, line); err != nil {
			if torn {
				c.persistErr = err
			}
			return err
		}
	}
	c.entries, c.size, c.state = trial.entries, trial.size, trial.state
	return nil
}

// writeFile is the single write of an appended line; tests replace it to
// simulate torn writes.
var writeFile = func(f *os.File, b []byte) (int, error) { return f.Write(b) }

// appendLine appends line in one write and fsyncs. On any failure it
// truncates the file back to its previous size so no torn line remains.
//
// torn is true when the rollback itself failed, so the file may end in a
// partial line.
func appendLine(path string, line []byte) (torn bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return false, err
	}
	size := st.Size()
	if _, err = f.Seek(size, 0); err == nil {
		_, err = writeFile(f, line)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if terr := truncateFile(path, size); terr != nil {
			return true, errors.Join(err, fmt.Errorf("chain: rollback failed: %w", terr))
		}
		return false, err
	}
	return false, nil
}

// truncateFile rolls a failed append back; tests replace it.
var truncateFile = os.Truncate

// SetPath writes the whole chain to path atomically and persists later
// appends there.
func (c *Chain) SetPath(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".chain-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	w := bufio.NewWriter(tmp)
	for _, e := range c.entries {
		var line []byte
		if line, err = marshalLine(e); err != nil {
			break
		}
		if _, err = w.Write(line); err != nil {
			break
		}
	}
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
		return err
	}
	c.path = path
	c.persistErr = nil // the file was just rewritten whole
	return nil
}

// Head returns the last entry.
func (c *Chain) Head() Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneEntry(c.entries[len(c.entries)-1])
}

// Entries returns a copy of all entries.
func (c *Chain) Entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.since(0, true)
}

// Since returns the entries with Seq greater than seq.
func (c *Chain) Since(seq uint64) []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.since(seq, false)
}

func (c *Chain) since(seq uint64, all bool) []Entry {
	var out []Entry
	for _, e := range c.entries {
		if all || e.Seq > seq {
			out = append(out, cloneEntry(e))
		}
	}
	return out
}

// State returns a copy of the current state.
func (c *Chain) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.clone()
}

// ID returns the server ID.
func (c *Chain) ID() ID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.ID
}
