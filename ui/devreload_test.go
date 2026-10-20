package ui

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/theme"
)

const validKage = `//kage:unit pixels

package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	return imageSrc0At(src)
}
`

// gfxJob is one unit of work to run on ebiten's game goroutine.
type gfxJob struct {
	fn   func()
	done chan struct{}
}

// gfx is the process-wide ebiten game. ebiten.RunGame may only be called once
// per process, so every graphics-needing test submits a job to this single
// game instead of running its own (R4). It gives ebiten.NewShader the graphics
// context it needs, which `go test` otherwise lacks.
var gfx struct {
	once   sync.Once
	jobs   chan gfxJob
	exited chan struct{} // closed when RunGame returns (no display, etc.)
	err    error
}

type shaderGame struct{}

func (shaderGame) Update() error {
	for {
		select {
		case j := <-gfx.jobs:
			j.fn()
			close(j.done)
		default:
			return nil
		}
	}
}
func (shaderGame) Draw(*ebiten.Image)         {}
func (shaderGame) Layout(w, h int) (int, int) { return 16, 16 }

// withGraphics runs fn on the shared ebiten game goroutine, skipping the test
// when no display is available.
func withGraphics(t *testing.T, fn func()) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("ebiten.RunGame must own the main thread on darwin")
	}
	gfx.once.Do(func() {
		gfx.jobs = make(chan gfxJob)
		gfx.exited = make(chan struct{})
		go func() {
			gfx.err = ebiten.RunGame(shaderGame{})
			close(gfx.exited)
		}()
	})
	j := gfxJob{fn: fn, done: make(chan struct{})}
	select {
	case gfx.jobs <- j:
	case <-gfx.exited:
		t.Skipf("no display for ebiten: %v", gfx.err)
	}
	select {
	case <-j.done:
	case <-gfx.exited:
		t.Skipf("no display for ebiten: %v", gfx.err)
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
