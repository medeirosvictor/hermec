// Package update checks GitHub for a newer hermec release. Every failure is
// silent: the app must behave identically offline.
package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// endpoint is a var so tests can point it at a stub server.
var endpoint = "https://api.github.com/repos/medeirosvictor/hermec/releases/latest"

const (
	timeout  = 3 * time.Second
	maxBody  = 64 << 10
	devBuild = "dev"
)

// Available describes a newer release.
type Available struct {
	Version string // tag, e.g. "v0.2.0"
	URL     string // release page
}

// Check reports whether a release newer than current exists. It returns false
// for dev builds (without making any request), network or HTTP errors,
// malformed responses, and when the remote is not strictly newer.
func Check(ctx context.Context, current string) (Available, bool) {
	if current == devBuild {
		return Available{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Available{}, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Available{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Available{}, false
	}
	// A body over the cap is truncated, hence invalid JSON, hence rejected.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Available{}, false
	}
	var rel struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return Available{}, false
	}
	if !newer(rel.Tag, current) {
		return Available{}, false
	}
	return Available{Version: rel.Tag, URL: rel.URL}, true
}

// parse reads strict vMAJOR.MINOR.PATCH. Hyphenated tags (prereleases) are
// rejected as malformed: /releases/latest never returns prereleases, so one
// appearing here is unexpected and not worth announcing.
func parse(v string) ([3]int, bool) {
	var out [3]int
	if !strings.HasPrefix(v, "v") {
		return out, false
	}
	parts := strings.Split(v[1:], ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" {
			return out, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return out, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether remote is strictly greater than local. Malformed
// input on either side yields false.
func newer(remote, local string) bool {
	r, ok := parse(remote)
	if !ok {
		return false
	}
	l, ok := parse(local)
	if !ok {
		return false
	}
	for i := range r {
		if r[i] != l[i] {
			return r[i] > l[i]
		}
	}
	return false
}
