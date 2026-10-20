package update

import "testing"

func TestReleaseURLPrefix(t *testing.T) {
	const base = "https://github.com/medeirosvictor/hermec/releases/"
	good := base + "tag/v9.9.9"
	cases := map[string]string{
		good:                            good,
		base:                            base,
		base + "tag/v1.0.0%2Brc":        base + "tag/v1.0.0%2Brc",
		"https://github.com/evil/phish": fallbackURL,
		"https://github.com/medeirosvictor/hermec/issues/1":           fallbackURL,
		"https://github.com/medeirosvictor/hermec/releasesX":          fallbackURL,
		"http://github.com/medeirosvictor/hermec/releases/tag/v9.9.9": fallbackURL,
		// traversal out of the releases path
		base + "../../evil":         fallbackURL,
		base + "tag/../../../evil":  fallbackURL,
		base + "%2e%2e/%2e%2e/evil": fallbackURL,
		base + "%2E%2E/evil":        fallbackURL,
		base + "..%2fevil":          fallbackURL,
		base + "tag/..\\..\\evil":   fallbackURL,
		// authority tricks
		"https://github.com@evil.example/medeirosvictor/hermec/releases/x": fallbackURL,
		"https://user:pw@github.com/medeirosvictor/hermec/releases/x":      fallbackURL,
		"https://github.com.evil.example/medeirosvictor/hermec/releases/x": fallbackURL,
		"https://github.com:8443/medeirosvictor/hermec/releases/x":         fallbackURL,
		"https://GITHUB.com/medeirosvictor/hermec/releases/x":              fallbackURL, // strict: exact lowercase host only
		"https://github.com/medeirosvictor/hermec-evil/releases/x":         fallbackURL,
		"https://github.com/MedeirosVictor/hermec/releases/x":              fallbackURL,
		"https://github.com//medeirosvictor/hermec/releases/x":             fallbackURL,
		"": fallbackURL,
	}
	for in, want := range cases {
		if got := releaseURL(in); got != want {
			t.Errorf("releaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
