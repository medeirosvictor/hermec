package update

import "testing"

func TestReleaseURLPrefix(t *testing.T) {
	good := "https://github.com/medeirosvictor/hermec/releases/tag/v9.9.9"
	cases := map[string]string{
		good:                            good,
		"https://github.com/evil/phish": fallbackURL,
		"https://github.com/medeirosvictor/hermec/issues/1":           fallbackURL,
		"https://github.com/medeirosvictor/hermec/releasesX":          fallbackURL,
		"http://github.com/medeirosvictor/hermec/releases/tag/v9.9.9": fallbackURL,
	}
	for in, want := range cases {
		if got := releaseURL(in); got != want {
			t.Errorf("releaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
