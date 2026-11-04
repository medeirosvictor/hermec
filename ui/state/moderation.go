package state

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// adminRole is the builtin role "Make admin" grants.
const adminRole = "admin"

// ModKind is one moderation action on a person.
type ModKind int

const (
	ModBan ModKind = iota
	ModMakeAdmin
	ModRemoveAdmin
	ModUnban
)

// ModItem is one moderation action: what to show and the chain entry to
// append. Menus keep only Kind and Target and rebuild it with ModAction when
// it runs.
type ModItem struct {
	Kind    ModKind
	Label   string
	Confirm bool // ask first (a ban)
	Op      chain.Op
	Body    any
	Target  string // fingerprint acted on
}

// IsManager reports whether fp may moderate: the founder, or an unbanned
// holder of the manage permission. The server enforces the same rule.
func IsManager(cs chain.State, fp string) bool {
	return fp != "" && cs.Can(fp, chain.OpBan)
}

func hasRoleName(roles []string, role string) bool { return slices.Contains(roles, role) }

// ModItems are the moderation items a viewer sees for target: none unless the
// viewer is a manager, the target is someone else, not the founder and not
// already banned. Role changes keep the target's other roles.
func ModItems(cs chain.State, viewer, target string) []ModItem {
	if target == "" || viewer == target || target == cs.Founder || cs.Banned(target) || !IsManager(cs, viewer) {
		return nil
	}
	cur := cs.Roles().RolesFor(target)
	var items []ModItem
	if hasRoleName(cur, adminRole) {
		rest := slices.DeleteFunc(slices.Clone(cur), func(r string) bool { return r == adminRole })
		it := ModItem{Kind: ModRemoveAdmin, Label: "Remove admin", Op: chain.OpGrant,
			Body: chain.GrantBody{Fingerprint: target, Roles: rest}, Target: target}
		if sameRoles(rest, cs.Roles().DefaultRoles) { // empty, or what a member gets anyway
			// Revoking drops the grant, so the member follows the server's defaults.
			it.Op, it.Body = chain.OpRevoke, chain.FPBody{Fingerprint: target}
		}
		items = append(items, it)
	} else {
		items = append(items, ModItem{Kind: ModMakeAdmin, Label: "Make admin", Op: chain.OpGrant,
			Body: chain.GrantBody{Fingerprint: target, Roles: append(slices.Clone(cur), adminRole)}, Target: target})
	}
	items = append(items, ModItem{Kind: ModBan, Label: "Ban…", Confirm: true, Op: chain.OpBan,
		Body: chain.FPBody{Fingerprint: target}, Target: target})
	return items
}

// sameRoles reports whether rest is empty or holds the same roles as def,
// ignoring order.
func sameRoles(rest, def []string) bool {
	if len(rest) == 0 {
		return true
	}
	a, b := slices.Clone(rest), slices.Clone(def)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// ModAction rebuilds the action of the given kind on target from the current
// chain state, so a menu or dialog opened earlier never acts on stale roles or
// ban state. ok is false when the action no longer applies.
func ModAction(cs chain.State, viewer string, kind ModKind, target string) (ModItem, bool) {
	if kind == ModUnban {
		if target == "" || !IsManager(cs, viewer) || !cs.Banned(target) {
			return ModItem{}, false
		}
		return unbanItem(target), true
	}
	for _, it := range ModItems(cs, viewer, target) {
		if it.Kind == kind {
			return it, true
		}
	}
	return ModItem{}, false
}

func unbanItem(fp string) ModItem {
	return ModItem{Kind: ModUnban, Label: "Unban", Op: chain.OpUnban, Body: chain.FPBody{Fingerprint: fp}, Target: fp}
}

// BannedRow is one line of the banned-members list.
type BannedRow struct {
	FP    string
	Label string // last-known name with the fingerprint, or the fingerprint alone
	Item  ModItem
}

// KnownNames maps fingerprints to the last name seen for them in the roster,
// voice occupants and chat history of st.
func KnownNames(st *State) map[string]string {
	names := map[string]string{}
	for _, msgs := range st.Messages {
		for _, m := range msgs {
			if m.FromFP != "" && m.FromName != "" {
				names[m.FromFP] = m.FromName
			}
		}
	}
	for _, vs := range st.Voice {
		for _, v := range vs {
			if v.Name != "" {
				names[v.Fingerprint] = v.Name
			}
		}
	}
	for _, m := range st.Roster {
		if m.Name != "" {
			names[m.Fingerprint] = m.Name
		}
	}
	return names
}

// BannedRows lists the chain's banned fingerprints for a manager viewer, with
// an Unban item each, sorted by label. Nil for a non-manager.
func BannedRows(cs chain.State, viewer string, names map[string]string) []BannedRow {
	if !IsManager(cs, viewer) {
		return nil
	}
	var rows []BannedRow
	for fp := range cs.Bans {
		label := fp
		if n := CleanName(names[fp], MaxServerNameRunes); n != "" {
			label = n + " (" + fp + ")"
		}
		rows = append(rows, BannedRow{FP: fp, Label: label, Item: unbanItem(fp)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Label != rows[j].Label {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].FP < rows[j].FP
	})
	return rows
}

// ModOutcome is the status line for a finished moderation action on the
// person called name: "banned X" or "couldn't ban X: you don't have
// permission". The raw error is for the log, not for here.
func ModOutcome(it ModItem, name string, err error) string {
	done, verb := "", ""
	switch it.Kind {
	case ModBan:
		done, verb = "banned", "ban"
	case ModUnban:
		done, verb = "unbanned", "unban"
	case ModMakeAdmin:
		done, verb = "made admin:", "make admin"
	case ModRemoveAdmin:
		done, verb = "removed admin:", "remove admin"
	}
	if err == nil {
		return done + " " + name
	}
	return fmt.Sprintf("couldn't %s %s: %s", verb, name, modReason(err))
}

func modReason(err error) string {
	var se *client.ServerError
	if errors.As(err, &se) {
		switch se.Code {
		case proto.CodeForbidden:
			return "you don't have permission"
		case proto.CodeChainConflict:
			return "the server was busy, try again"
		case proto.CodeNotActive:
			return "the host isn't serving right now"
		}
	}
	return "the request failed"
}
