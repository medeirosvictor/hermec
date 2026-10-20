package ui

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// healthEvery is how often the session log gets a call-health line.
const healthEvery = 30 * time.Second

// consoleLog is stderr only: verbose breadcrumbs print here so the std logger,
// which also feeds the session file, does not write them to the file twice.
var consoleLog = log.New(os.Stderr, "", log.LstdFlags)

// setLogWriter makes w the session log file: every logf line goes to it,
// verbose or not. The console is unaffected.
func (g *game) setLogWriter(w io.Writer) {
	g.fileLog = log.New(w, "", log.LstdFlags)
}

// logf writes a breadcrumb to the session log file, and to the console only
// with -v. Each sink gets the line exactly once.
func (g *game) logf(format string, args ...any) {
	if g.fileLog != nil {
		g.fileLog.Printf(format, args...)
	}
	if g.opts.Verbose {
		consoleLog.Printf(format, args...)
	}
}

// serverLogf routes cfg's server log lines through the standard logger (the
// console and, in the app, the session file) with a "[server] " prefix.
func serverLogf(cfg *server.Config) {
	cfg.Logf = func(format string, args ...any) {
		log.Printf("[server] "+format, args...)
	}
}

// logStartup writes the first lines of a session log. Device names are the
// saved choices; empty means the system default.
func (g *game) logStartup() {
	g.logf("platform %s/%s, %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	g.logf("audio input %q, output %q", deviceOrDefault(g.set.InputDevice), deviceOrDefault(g.set.OutputDevice))
}

func deviceOrDefault(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// healthLine is the periodic call-health line: ping, then each person's
// jitter and concealment. It carries names only, never chat text.
func healthLine(rtt time.Duration, rttOK bool, parts []state.Participant) string {
	var b strings.Builder
	b.WriteString("health: ping ")
	if !rttOK {
		b.WriteString("no data")
	} else {
		fmt.Fprintf(&b, "%d ms", rtt.Round(time.Millisecond)/time.Millisecond)
	}
	for _, p := range parts {
		if !p.OK {
			fmt.Fprintf(&b, "; %s no data", p.Name)
			continue
		}
		fmt.Fprintf(&b, "; %s jitter %d ms concealed %.1f%%", p.Name, p.Jitter.Round(time.Millisecond)/time.Millisecond, p.Concealed*100)
	}
	return b.String()
}

// stepHealth logs a health line every healthEvery while in a call. It runs
// after stepQuality, whose participant list it reuses.
func (g *game) stepHealth(now time.Time) {
	s := g.callSess()
	if s == nil || s.st.InCall == "" {
		g.nextHealth = time.Time{}
		return
	}
	if g.nextHealth.IsZero() {
		g.nextHealth = now.Add(healthEvery)
		return
	}
	if now.Before(g.nextHealth) {
		return
	}
	g.nextHealth = now.Add(healthEvery)
	rtt, ok := g.rttOf()
	line := healthLine(rtt, ok, g.q.parts)
	if (g.share.on || g.watch.fp != "") && s.c != nil {
		send, recv, fps := s.c.VideoStats()
		line += videoStatsSuffix(g.share.on, g.watch.fp != "", send, recv, fps)
	}
	g.logf("%s", line)
}

// openLogFolder opens the log directory in the file manager.
func (g *game) openLogFolder() {
	if g.opts.LogDir == "" {
		g.notice = "no log folder (file logging is off)"
		return
	}
	_ = os.MkdirAll(g.opts.LogDir, 0o700)
	open := g.openFn
	if open == nil {
		open = openURL
	}
	g.notice = g.opts.LogDir
	open(g.opts.LogDir)
}
