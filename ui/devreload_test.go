package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

const validKage = `//kage:unit pixels

package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	return imageSrc0At(src)
}
`

// shaderGame is a minimal ebiten game; running it for one frame gives
// ebiten.NewShader the graphics context it needs, which `go test` otherwise
// lacks (R4).
type shaderGame struct{ run func() }

func (g *shaderGame) Update() error {
	g.run()
	return ebiten.Termination
}
func (g *shaderGame) Draw(*ebiten.Image)         {}
func (g *shaderGame) Layout(w, h int) (int, int) { return 16, 16 }

// withGraphics runs fn inside a one-frame ebiten game, skipping the test when
// no display is available.
func withGraphics(t *testing.T, fn func()) {
	t.Helper()
	ran := false
	err := ebiten.RunGame(&shaderGame{run: func() { ran = true; fn() }})
	if !ran {
		t.Skipf("no display for ebiten: %v", err)
	}
}

func TestDevReloadBadShaderKeepsOld(t *testing.T) {
	withGraphics(t, func() {
		var c crt
		if err := c.setSource([]byte(validKage)); err != nil {
			t.Fatalf("valid source: %v", err)
		}
		first := c.shader
		if first == nil {
			t.Fatal("shader nil after valid setSource")
		}
		if err := c.setSource([]byte("package main\nfunc Fragment( {")); err == nil {
			t.Fatal("invalid source compiled")
		}
		if c.shader != first {
			t.Fatal("bad source replaced the shader")
		}
		if err := c.setSource([]byte(validKage)); err != nil {
			t.Fatalf("second valid source: %v", err)
		}
		if c.shader == first || c.shader == nil {
			t.Fatal("valid source did not swap the shader")
		}
	})
}

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
	g := &game{th: theme.Default(), st: state.New()}
	d := newTestDev(g, files, mt)
	d.poll(t0) // baselines

	// Change font_size so the edit is observable in g.th.
	bumped := string(src) + fmt.Sprintf("\nfont_size = %v\n", g.th.FontSize+4)
	files[path] = bumped
	mt[path] = t0.Add(time.Minute)
	d.poll(t0.Add(2 * time.Second))

	if g.th.FontSize != theme.Default().FontSize+4 {
		t.Fatalf("font_size = %v, edit not applied (status %q)", g.th.FontSize, g.st.Status)
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
	g := &game{th: theme.Default(), st: state.New()}
	before := g.th
	d := newTestDev(g, files, mt)
	d.poll(t0)
	mt[path] = t0.Add(time.Minute)
	d.poll(t0.Add(2 * time.Second))
	if g.th != before {
		t.Fatal("theme changed on parse error")
	}
	if !strings.HasPrefix(g.st.Status, "dev reload "+path+": ") {
		t.Fatalf("status = %q", g.st.Status)
	}
}

func TestDevReloadMissingPanelShaderHarmless(t *testing.T) {
	g := &game{th: theme.Default(), st: state.New()}
	d := newTestDev(g, map[string]string{}, map[string]time.Time{})
	d.register("ui/panel.kage", func([]byte) error { return errors.New("must not run") })
	d.poll(time.Now())
	d.poll(time.Now().Add(2 * time.Second))
	if g.st.Status != "" {
		t.Fatalf("status = %q", g.st.Status)
	}
}
