// Command hermec is the Hermec GUI chat client.
package main

import (
	"flag"
	"log"

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
	flag.Parse()

	log.Printf("hermec %s", version.Version)

	if err := ui.Run(ui.Options{
		ServerURL: *server, Name: *name, KeyPath: *key, ThemePath: *themePath,
		Local: *local, Verbose: *verbose,
	}); err != nil {
		log.Fatal(err)
	}
}
