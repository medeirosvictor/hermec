// Package gfxtest runs test bodies on a shared ebiten game goroutine, giving
// them the graphics context that `go test` otherwise lacks.
package gfxtest

import (
	"runtime"
	"sync"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

// job is one unit of work to run on ebiten's game goroutine.
type job struct {
	fn   func()
	done chan struct{}
}

// gfx is the process-wide ebiten game. ebiten.RunGame may only be called once
// per process, so every graphics-needing test submits a job to this single
// game instead of running its own (R4). It gives ebiten.NewShader the graphics
// context it needs.
var gfx struct {
	once   sync.Once
	jobs   chan job
	exited chan struct{} // closed when RunGame returns (no display, etc.)
	err    error
}

type game struct{}

func (game) Update() error {
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
func (game) Draw(*ebiten.Image)         {}
func (game) Layout(w, h int) (int, int) { return 16, 16 }

// Run runs fn on the shared ebiten game goroutine, skipping the test when no
// display is available.
func Run(t *testing.T, fn func()) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("ebiten.RunGame must own the main thread on darwin")
	}
	gfx.once.Do(func() {
		gfx.jobs = make(chan job)
		gfx.exited = make(chan struct{})
		go func() {
			gfx.err = ebiten.RunGame(game{})
			close(gfx.exited)
		}()
	})
	j := job{fn: fn, done: make(chan struct{})}
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
