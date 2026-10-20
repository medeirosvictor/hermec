package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/discover"
)

// authNamed authenticates id under name and returns the connection after
// consuming auth_ok. The caller reads the roster frames itself.
func authNamed(t *testing.T, url string, id *identity.Identity, name string) *websocket.Conn {
	t.Helper()
	c := dial(t, url)
	nonce := readChallenge(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{PubKey: id.PublicKey(), Name: name, Sig: id.Sign(nonce)})
	if env := readEnv(t, c); env.Type != proto.TypeAuthOK {
		t.Fatalf("auth type = %q", env.Type)
	}
	return c
}

func readRoster(t *testing.T, c *websocket.Conn) proto.Roster {
	t.Helper()
	env := readEnv(t, c)
	if env.Type != proto.TypeRoster {
		t.Fatalf("type = %q, want roster", env.Type)
	}
	var r proto.Roster
	if err := json.Unmarshal(env.Data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAuthOKCarriesServerName(t *testing.T) {
	for _, name := range []string{"Primos", ""} {
		cfg := chatCfg()
		cfg.ServerName = name
		url := startServer(t, cfg)
		_, env := authAs(t, url, mustID(t), "")
		var ok proto.AuthOK
		if err := json.Unmarshal(env.Data, &ok); err != nil {
			t.Fatal(err)
		}
		if ok.ServerName != name {
			t.Errorf("ServerName = %q, want %q", ok.ServerName, name)
		}
	}
}

func TestRosterOnConnectBroadcastsToBoth(t *testing.T) {
	cfg := chatCfg()
	url := startServer(t, cfg)
	idA, idB := mustID(t), mustID(t)
	a := authNamed(t, url, idA, "alice")
	if r := readRoster(t, a); len(r.Members) != 1 || r.Total != 1 {
		t.Fatalf("own roster = %+v", r)
	}
	b := authNamed(t, url, idB, "bob")
	for _, c := range []*websocket.Conn{a, b} {
		r := readRoster(t, c)
		if len(r.Members) != 2 || r.Total != 2 {
			t.Fatalf("roster = %+v, want 2 members", r)
		}
		for i, m := range r.Members {
			id, name := idA, "alice"
			if i == 1 {
				id, name = idB, "bob"
			}
			fp := identity.Fingerprint(id.PublicKey())
			if m.Name != name || m.Fingerprint != fp || !reflect.DeepEqual(m.Roles, cfg.Roles.RolesFor(fp)) {
				t.Errorf("member %d = %+v", i, m)
			}
		}
	}
}

func TestRosterOnDisconnect(t *testing.T) {
	url := startServer(t, chatCfg())
	a := authNamed(t, url, mustID(t), "alice")
	readRoster(t, a)
	b := authNamed(t, url, mustID(t), "bob")
	readRoster(t, a)
	readRoster(t, b)
	b.Close()
	if r := readRoster(t, a); len(r.Members) != 1 || r.Total != 1 {
		t.Fatalf("roster after disconnect = %+v", r)
	}
}

// drainAll reads c in the background, handing every roster frame (raw and
// decoded) to fn. Clients must keep reading or the send buffer overflows.
func drainAll(c *websocket.Conn, fn func(raw []byte, r proto.Roster)) {
	_ = c.SetReadDeadline(time.Time{})
	go func() {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			env, err := proto.Decode(raw)
			if err != nil || env.Type != proto.TypeRoster {
				continue
			}
			var r proto.Roster
			if json.Unmarshal(env.Data, &r) == nil {
				fn(raw, r)
			}
		}
	}()
}

func TestRosterTruncatesAtMax(t *testing.T) {
	cfg := chatCfg()
	cfg.MaxPerIP = 1000 // every client is 127.0.0.1; the per-IP cap would refuse most
	url := startServer(t, cfg)
	n := proto.MaxRosterMembers + 5
	var mu sync.Mutex
	var last proto.Roster
	for i := 0; i < n; i++ {
		c := authNamed(t, url, mustID(t), fmt.Sprintf("n%03d", i))
		if i == 0 {
			drainAll(c, func(_ []byte, r proto.Roster) {
				mu.Lock()
				last = r
				mu.Unlock()
			})
		} else {
			drainAll(c, func([]byte, proto.Roster) {})
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		mu.Lock()
		r := last
		mu.Unlock()
		if r.Total == n {
			if len(r.Members) != proto.MaxRosterMembers {
				t.Fatalf("len(Members) = %d, want %d", len(r.Members), proto.MaxRosterMembers)
			}
			for i, m := range r.Members {
				if want := fmt.Sprintf("n%03d", i); m.Name != want {
					t.Fatalf("member %d = %q, want %q", i, m.Name, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("last roster Total = %d, want %d", r.Total, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRosterStaysUnderMaxMessageSize(t *testing.T) {
	url := startServer(t, chatCfg())
	var mu sync.Mutex
	var frames [][]byte
	var rosters []proto.Roster
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("%c", 'a'+i)
		for len(name) < 25000 {
			name += "x"
		}
		c := authNamed(t, url, mustID(t), name)
		if i == 0 {
			drainAll(c, func(raw []byte, r proto.Roster) {
				mu.Lock()
				frames = append(frames, raw)
				rosters = append(rosters, r)
				mu.Unlock()
			})
		} else {
			drainAll(c, func([]byte, proto.Roster) {})
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := len(rosters) > 0 && rosters[len(rosters)-1].Total == 4
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw a roster with Total 4")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, raw := range frames {
		if len(raw) > proto.MaxMessageSize {
			t.Errorf("frame %d is %d bytes, over MaxMessageSize", i, len(raw))
		}
	}
	r := rosters[len(rosters)-1]
	if len(r.Members) != 4 {
		t.Fatalf("len(Members) = %d, want 4", len(r.Members))
	}
	names := make([]string, len(r.Members))
	for i, m := range r.Members {
		names[i] = m.Name
		if m.Name[0] != byte('a'+i) {
			t.Errorf("member %d name starts %q, want %q", i, m.Name[0], byte('a'+i))
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Error("members not sorted")
	}
}

func TestRosterCapsLongNames(t *testing.T) {
	url := startServer(t, chatCfg())
	long := "!" + strings.Repeat("x", 24999)
	authNamed(t, url, mustID(t), long)
	authNamed(t, url, mustID(t), "bob")
	c := authNamed(t, url, mustID(t), "carol")
	r := readRoster(t, c)
	if len(r.Members) != 3 || r.Total != 3 {
		t.Fatalf("roster = %d members, total %d; want 3 and 3", len(r.Members), r.Total)
	}
	if n := utf8.RuneCountInString(r.Members[0].Name); n != discover.MaxNameRunes || r.Members[0].Name[0] != '!' {
		t.Errorf("first name has %d runes (starts %q), want %d starting '!'", n, r.Members[0].Name[0], discover.MaxNameRunes)
	}
	if r.Members[1].Name != "bob" || r.Members[2].Name != "carol" {
		t.Errorf("other names = %q, %q", r.Members[1].Name, r.Members[2].Name)
	}
}

func TestRosterSameIdentityTwice(t *testing.T) {
	url := startServer(t, chatCfg())
	id := mustID(t)
	a := authNamed(t, url, id, "alice")
	if r := readRoster(t, a); r.Total != 1 {
		t.Fatalf("first roster = %+v", r)
	}
	b := authNamed(t, url, id, "alice")
	for _, c := range []*websocket.Conn{a, b} {
		if r := readRoster(t, c); r.Total != 2 || len(r.Members) != 2 {
			t.Fatalf("roster = %+v, want 2 entries", r)
		}
	}
	b.Close()
	if r := readRoster(t, a); r.Total != 1 || len(r.Members) != 1 {
		t.Fatalf("roster after close = %+v", r)
	}
}
