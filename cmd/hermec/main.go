// Command hermec is the Hermec GUI chat client.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/medeirosvictor/hermec/applog"
	"github.com/medeirosvictor/hermec/ui"
	"github.com/medeirosvictor/hermec/version"
)

func main() {
	server := flag.String("server", "ws://localhost:7697/", "server URL")
	name := flag.String("name", "", `display name (default "anon-" + short fingerprint)`)
	key := flag.String("key", "", "identity key path (default <user config dir>/hermec/identity.key)")
	themePath := flag.String("theme", "", "optional theme TOML file")
	local := flag.Bool("local", false, "ignore -server and run an in-process server")
	verbose := flag.Bool("v", false, "log breadcrumbs")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("hermec %s\n", version.Version)
		return
	}

	var logFile io.Writer
	var logDir string
	if cfgDir, err := os.UserConfigDir(); err == nil {
		dir := applog.Dir(cfgDir)
		if f, path, err := applog.Open(dir, time.Now(), 10); err != nil {
			log.Printf("session log disabled: %v", err)
		} else {
			defer f.Close()
			logFile, logDir = f, dir
			log.SetOutput(fileFirstWriter{file: f, console: os.Stderr})
			log.Printf("session log: %s", path)
		}
	} else {
		log.Printf("session log disabled: %v", err)
	}

	log.Printf("hermec %s", version.Version)

	if err := ui.Run(ui.Options{
		ServerURL: *server, Name: *name, KeyPath: *key, ThemePath: *themePath,
		Local: *local, Verbose: *verbose, LogDir: logDir, LogFile: logFile,
	}); err != nil {
		log.Fatal(err)
	}
}

// fileFirstWriter writes every line to the session file first and then to the
// console, and ignores console errors (stderr may be invalid in a GUI-subsystem
// build), so the file always gets the line. It reports the file's error only.
type fileFirstWriter struct{ file, console io.Writer }

func (w fileFirstWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	_, _ = w.console.Write(p)
	return n, err
}
