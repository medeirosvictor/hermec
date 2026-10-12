package ui

import (
	"fmt"
	"os"
	"time"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// devReload is the HERMEC_DEV=1 live-reload: it polls source files (paths are
// relative to the working directory, so run from the repo root) and applies
// edits to the running window. A failed reload reports to the status line and
// keeps the previous version.
//
// To live-reload another shader, add one line in newDevReload:
//
//	d.register("ui/panel.kage", g.panel.setSource)
type devReload struct {
	g        *game
	w        state.Watcher
	read     func(path string) ([]byte, error)
	handlers map[string]func(src []byte) error
}

// newDevReload returns the reloader with the standard watches registered.
func newDevReload(g *game) *devReload {
	d := &devReload{
		g:        g,
		w:        state.Watcher{Stat: statMtime},
		read:     os.ReadFile,
		handlers: map[string]func([]byte) error{},
	}
	d.register("ui/crt.kage", g.crt.setSource)
	d.register("ui/panel.kage", g.panel.setSource)
	// Every preset is watched, but an edit applies only while it is the active
	// one, so cycling palettes with F2 needs no re-registration.
	for _, p := range theme.Presets() {
		name := p.Name
		d.register("ui/theme/presets/"+name+".toml", func(src []byte) error {
			if g.th.Name != name {
				return nil
			}
			th, err := theme.Parse(name, src)
			if err != nil {
				return err
			}
			return g.applyDevTheme(th)
		})
	}
	if path := g.opts.ThemePath; path != "" {
		d.register(path, func(src []byte) error {
			th, err := theme.Parse("", src)
			if err != nil {
				return err
			}
			return g.applyDevTheme(th)
		})
	}
	return d
}

// devFromEnv returns a reloader when HERMEC_DEV is set, else nil.
func devFromEnv(g *game) *devReload {
	if os.Getenv("HERMEC_DEV") == "" {
		return nil
	}
	return newDevReload(g)
}

// register watches path and calls fn with the file's contents when it changes.
// The file need not exist yet.
func (d *devReload) register(path string, fn func(src []byte) error) {
	d.handlers[path] = fn
	d.w.Add(path)
}

// poll applies any changed files. Cheap enough to call every Update: the
// watcher touches the disk at most once per second.
func (d *devReload) poll(now time.Time) {
	for _, path := range d.w.Changed(now) {
		src, err := d.read(path)
		if err == nil {
			err = d.handlers[path](src)
		}
		if err != nil {
			d.g.cur().st.Status = fmt.Sprintf("dev reload %s: %v", path, err)
			continue
		}
		d.g.logf("dev reload %s", path)
	}
}

func statMtime(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// applyDevTheme swaps in th, rebuilding the text faces only if font_size
// changed. crtOn is separate state and is left alone.
func (g *game) applyDevTheme(th theme.Theme) error {
	if th.FontSize != g.th.FontSize || g.face == nil {
		face, err := theme.Face(th.FontSize)
		if err != nil {
			return err
		}
		faceS, err := theme.Face(th.FontSize * smallScale)
		if err != nil {
			return err
		}
		faceT, err := theme.Face(th.FontSize * titleScale)
		if err != nil {
			return err
		}
		g.face, g.faceS, g.faceT = face, faceS, faceT
	}
	g.th = th
	return nil
}
