package state

import (
	"reflect"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func mem(name string, roles ...string) proto.Member {
	return proto.Member{Fingerprint: "fp-" + name, Name: name, Roles: roles}
}

func TestRosterGroups(t *testing.T) {
	ms := []proto.Member{
		mem("a", "admin"), mem("ab", "admin", "bot"), mem("b", "bot"),
		mem("u", "user"), mem("n"), mem("c", "custom"),
	}
	g := RosterGroups(ms)
	if len(g) != 3 || g[0].Title != "Admins" || g[1].Title != "Members" || g[2].Title != "Bots" {
		t.Fatalf("groups = %+v", g)
	}
	names := func(m []proto.Member) []string {
		var o []string
		for _, x := range m {
			o = append(o, x.Name)
		}
		return o
	}
	if got := names(g[0].Members); !reflect.DeepEqual(got, []string{"a", "ab"}) {
		t.Errorf("admins = %v", got)
	}
	if got := names(g[1].Members); !reflect.DeepEqual(got, []string{"u", "n", "c"}) {
		t.Errorf("members = %v", got)
	}
	if got := names(g[2].Members); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("bots = %v", got)
	}
	if g := RosterGroups([]proto.Member{mem("a", "admin"), mem("u")}); len(g) != 2 {
		t.Errorf("no-bot groups = %+v", g)
	}
	if RosterGroups(nil) != nil {
		t.Error("nil input should give nil")
	}
}

func TestRosterRows(t *testing.T) {
	ms := []proto.Member{mem("a1", "admin"), mem("a2", "admin"), mem("m1")}
	want := []RosterRow{
		{RosterHeading, "ADMINS — 2", ""},
		{RosterMember, "a1", "fp-a1"},
		{RosterMember, "a2", "fp-a2"},
		{RosterHeading, "MEMBERS — 1", ""},
		{RosterMember, "m1", "fp-m1"},
	}
	if got := RosterRows(ms, 3); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v", got)
	}
	got := RosterRows(ms, 7)
	if last := got[len(got)-1]; last != (RosterRow{Kind: RosterMore, Text: "and 4 more"}) {
		t.Errorf("last = %+v", last)
	}
	if got := RosterRows(ms, 2); len(got) != len(want) {
		t.Errorf("total<=len added rows: %+v", got)
	}
}

func TestRosterRowsCleansNames(t *testing.T) {
	long := strings.Repeat("x", 200)
	rows := RosterRows([]proto.Member{mem("a\x1b[0mb"), mem(long), mem("\x00")}, 3)
	var texts []string
	for _, r := range rows {
		if r.Kind == RosterMember {
			texts = append(texts, r.Text)
		}
	}
	if texts[0] != "a[0mb" {
		t.Errorf("controls: %q", texts[0])
	}
	if len([]rune(texts[1])) != MaxServerNameRunes {
		t.Errorf("cap: %d", len([]rune(texts[1])))
	}
	if texts[2] != "(unnamed)" {
		t.Errorf("unnamed: %q", texts[2])
	}
}

func TestApplyRoster(t *testing.T) {
	s := New()
	ms := []proto.Member{mem("a"), mem("b")}
	s.Apply(client.Event{Roster: &proto.Roster{Members: ms, Total: 5}}, true)
	ms[0].Name = "changed"
	if !s.HasRoster || s.RosterTotal != 5 || len(s.Roster) != 2 || s.Roster[0].Name != "a" {
		t.Errorf("state = %+v", s)
	}
}

func TestSetConnectedClearsRoster(t *testing.T) {
	s := New()
	if s.HasRoster {
		t.Fatal("New has roster")
	}
	s.Apply(client.Event{Roster: &proto.Roster{Members: []proto.Member{mem("a")}, Total: 1}}, true)
	s.SetConnected(nil, "fp")
	if s.Roster != nil || s.RosterTotal != 0 || s.HasRoster {
		t.Errorf("not cleared: %+v", s)
	}
}
