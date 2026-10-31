package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/ui/theme"
)

// newTestDev builds a devReload over an in-memory file system.
func newTestDev(g *game, files map[string]string, mt map[string]time.Time) *devReload {
	d := newDevReload(g)
	d.w.Stat = func(p string) (time.Time, error) {
		t, ok := mt[p]
		if !ok {
			return time.Time{}, os.ErrNotExist
		}
		return t, nil
	}
	d.read = func(p string) ([]byte, error) {
		s, ok := files[p]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(s), nil
	}
	return d
}

func TestDevReloadPresetEditApplies(t *testing.T) {
	src, _ := theme.PresetSource("amber")
	path := "ui/theme/presets/amber.toml"
	files := map[string]string{path: string(src)}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mt := map[string]time.Time{path: t0}
	g := &game{th: theme.Default()}
	d := newTestDev(g, files, mt)
	d.poll(t0) // baselines

	// Change font_size so the edit is observable in g.th.
	bumped := string(src) + fmt.Sprintf("\nfont_size = %v\n", g.th.FontSize+4)
	files[path] = bumped
	mt[path] = t0.Add(time.Minute)
	d.poll(t0.Add(2 * time.Second))

	if g.th.FontSize != theme.Default().FontSize+4 {
		t.Fatalf("font_size = %v, edit not applied (status %q)", g.th.FontSize, g.cur().st.Status)
	}
	if g.th.Name != "amber" {
		t.Fatalf("name = %q", g.th.Name)
	}
	if g.face == nil || g.faceS == nil || g.faceT == nil {
		t.Fatal("faces not rebuilt after font_size change")
	}
}

func TestDevReloadBadThemeKeepsOldAndReportsStatus(t *testing.T) {
	path := "ui/theme/presets/amber.toml"
	files := map[string]string{path: "bg = [not valid"}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mt := map[string]time.Time{path: t0}
	g := &game{th: theme.Default()}
	before := g.th
	d := newTestDev(g, files, mt)
	d.poll(t0)
	mt[path] = t0.Add(time.Minute)
	d.poll(t0.Add(2 * time.Second))
	if g.th != before {
		t.Fatal("theme changed on parse error")
	}
	if !strings.HasPrefix(g.cur().st.Status, "dev reload "+path+": ") {
		t.Fatalf("status = %q", g.cur().st.Status)
	}
}

func TestDevReloadMissingPanelShaderHarmless(t *testing.T) {
	g := &game{th: theme.Default()}
	d := newTestDev(g, map[string]string{}, map[string]time.Time{})
	d.register("ui/panel.kage", func([]byte) error { return errors.New("must not run") })
	d.poll(time.Now())
	d.poll(time.Now().Add(2 * time.Second))
	if g.cur().st.Status != "" {
		t.Fatalf("status = %q", g.cur().st.Status)
	}
}
