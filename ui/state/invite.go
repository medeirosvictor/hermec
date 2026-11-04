package state

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/medeirosvictor/hermec/core/proto"
)

// Invite is everything a friend needs to join a server from one pasted line.
// Password is a secret: it is set only when the sender explicitly chose to
// include it, and an Invite with one must never be logged or stored.
type Invite struct {
	Addr     string // ws:// or wss:// address
	ServerID string // canonical chain ID the join is pinned to
	Name     string // optional display name
	Password string // optional, wss:// only
}

// Caps on a pasted invite. Every field is bounded before it is trusted.
const (
	MaxInviteBytes    = 2048
	maxInviteAddr     = 512
	maxInvitePassword = 256 // bytes, the dedicated password bound
	inviteScheme      = "hermec"
	inviteHost        = "join"
	inviteVersion     = "1"
)

var errInvalidInvite = errors.New("not a valid HERMEC invite link")

// validInviteAddr checks the address of an invite. wss:// must be the
// canonical root URL a dedicated join accepts; ws:// must be a plain host:port
// URL. Neither may carry userinfo, a query or a fragment.
func validInviteAddr(raw string) error {
	if raw == "" || len(raw) > maxInviteAddr || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errInvalidInvite
	}
	if strings.HasPrefix(raw, "wss://") {
		_, err := proto.CanonicalDedicatedWSS(raw)
		return err
	}
	if !strings.HasPrefix(raw, "ws://") {
		return errors.New("invite address must start with ws:// or wss://")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ws" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errInvalidInvite
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return errInvalidInvite
		}
	}
	return nil
}

// Validate checks every field of i and returns it with the name cleaned.
func (i Invite) Validate() (Invite, error) {
	if err := validInviteAddr(i.Addr); err != nil {
		return Invite{}, err
	}
	if _, err := CanonicalDedicatedServerID(i.ServerID); err != nil {
		return Invite{}, err
	}
	if !utf8.ValidString(i.Name) || utf8.RuneCountInString(i.Name) > MaxServerNameRunes ||
		strings.IndexFunc(i.Name, unicode.IsControl) >= 0 {
		return Invite{}, errInvalidInvite
	}
	if len(i.Password) > maxInvitePassword || !utf8.ValidString(i.Password) {
		return Invite{}, errInvalidInvite
	}
	if i.Password != "" && !strings.HasPrefix(i.Addr, "wss://") {
		return Invite{}, errors.New("a password is only allowed in a wss:// invite")
	}
	return i, nil
}

// FormatInvite renders i as hermec://join?v=1&addr=…&id=…[&name=…][&pw=…].
// The password is included only if i.Password is non-empty.
func FormatInvite(i Invite) (string, error) {
	i, err := i.Validate()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(inviteScheme + "://" + inviteHost + "?v=" + inviteVersion)
	b.WriteString("&addr=" + url.QueryEscape(i.Addr))
	b.WriteString("&id=" + url.QueryEscape(i.ServerID))
	if i.Name != "" {
		b.WriteString("&name=" + url.QueryEscape(i.Name))
	}
	if i.Password != "" {
		b.WriteString("&pw=" + url.QueryEscape(i.Password))
	}
	if b.Len() > MaxInviteBytes {
		return "", errInvalidInvite
	}
	return b.String(), nil
}

// ParseInvite strictly parses an invite link. Surrounding whitespace is
// ignored; anything else unexpected (scheme, host, version, unknown or
// repeated parameters, bad fields) is an error. Errors never echo the input,
// which may hold a password.
func ParseInvite(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > MaxInviteBytes {
		return Invite{}, errInvalidInvite
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != inviteScheme || u.Host != inviteHost || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.Fragment != "" || u.Opaque != "" || u.RawQuery == "" {
		return Invite{}, errInvalidInvite
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Invite{}, errInvalidInvite
	}
	for k, v := range q {
		switch k {
		case "v", "addr", "id", "name", "pw":
		default:
			return Invite{}, errors.New("invite link has an unsupported field")
		}
		if len(v) != 1 {
			return Invite{}, errInvalidInvite
		}
	}
	if v, ok := q["v"]; !ok || v[0] != inviteVersion {
		return Invite{}, errors.New("unsupported invite link version")
	}
	return Invite{Addr: q.Get("addr"), ServerID: q.Get("id"), Name: q.Get("name"), Password: q.Get("pw")}.Validate()
}
