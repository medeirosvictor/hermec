package state

import "strings"

// DefaultName derives a display name like "anon-k7mv" from a fingerprint.
func DefaultName(fingerprint string) string {
	short := strings.SplitN(fingerprint, "-", 2)[0]
	if short == "" {
		return "anon"
	}
	return "anon-" + short
}
