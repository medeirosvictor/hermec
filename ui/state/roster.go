package state

import (
	"fmt"
	"strings"

	"github.com/medeirosvictor/hermec/core/proto"
)

// RosterGroup is one titled section of the members panel.
type RosterGroup struct {
	Title   string // "Admins" | "Members" | "Bots"
	Members []proto.Member
}

func hasRole(m proto.Member, role string) bool {
	for _, r := range m.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// RosterGroups splits members into Admins, Members and Bots (in that order,
// empty groups skipped). A member lands in one group: admin beats bot beats
// member. Input order is kept inside a group.
func RosterGroups(members []proto.Member) []RosterGroup {
	var admins, others, bots []proto.Member
	for _, m := range members {
		switch {
		case hasRole(m, "admin"):
			admins = append(admins, m)
		case hasRole(m, "bot"):
			bots = append(bots, m)
		default:
			others = append(others, m)
		}
	}
	var out []RosterGroup
	for _, g := range []RosterGroup{{"Admins", admins}, {"Members", others}, {"Bots", bots}} {
		if len(g.Members) > 0 {
			out = append(out, g)
		}
	}
	return out
}

type RosterRowKind int

const (
	RosterHeading RosterRowKind = iota
	RosterMember
	RosterMore
)

// RosterRow is one drawable line of the members panel.
type RosterRow struct {
	Kind RosterRowKind
	Text string // "ADMINS — 2" / cleaned name / "and 3 more"
	FP   string // member rows only
}

// RosterRows flattens the grouped roster into rows. Names are cleaned for
// display; total > len(members) adds a trailing "and N more" row.
func RosterRows(members []proto.Member, total int) []RosterRow {
	var rows []RosterRow
	for _, g := range RosterGroups(members) {
		rows = append(rows, RosterRow{Kind: RosterHeading, Text: fmt.Sprintf("%s — %d", strings.ToUpper(g.Title), len(g.Members))})
		for _, m := range g.Members {
			name := CleanName(m.Name, MaxServerNameRunes)
			if name == "" {
				name = "(unnamed)"
			}
			rows = append(rows, RosterRow{Kind: RosterMember, Text: name, FP: m.Fingerprint})
		}
	}
	if total > len(members) {
		rows = append(rows, RosterRow{Kind: RosterMore, Text: fmt.Sprintf("and %d more", total-len(members))})
	}
	return rows
}
