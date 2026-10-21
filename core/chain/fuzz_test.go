package chain

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func FuzzFromEntries(f *testing.F) {
	if id, err := identity.Generate(); err == nil {
		g, _ := NewEntry(id, nil, OpGenesis, GenesisBody{Name: "s", Founder: id.PublicKey()}, t0)
		c, _ := NewEntry(id, &g, OpConfig, ConfigBody{Name: "s"}, t0)
		var buf bytes.Buffer
		for _, e := range []Entry{g, c} {
			j, _ := json.Marshal(e)
			buf.Write(j)
			buf.WriteByte('\n')
		}
		f.Add(buf.Bytes())
	}
	f.Add([]byte("{}\n{}"))
	f.Add([]byte(`{"seq":0,"author":"AAAA","op":"genesis","body":"e30="}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var es []Entry
		for _, ln := range bytes.Split(data, []byte("\n")) {
			var e Entry
			if json.Unmarshal(ln, &e) == nil {
				es = append(es, e)
			}
		}
		FromEntries(es) // must not panic
	})
}
