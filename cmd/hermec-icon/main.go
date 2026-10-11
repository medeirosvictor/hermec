// Command hermec-icon writes the app icon files: icon-256.png and icon.ico.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/medeirosvictor/hermec/ui/icon"
)

func main() {
	out := flag.String("out", "assets", "output directory")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	p, err := icon.PNG(256)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "icon-256.png"), p, 0o644); err != nil {
		log.Fatal(err)
	}
	ico, err := icon.ICO([]int{16, 32, 48, 256})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "icon.ico"), ico, 0o644); err != nil {
		log.Fatal(err)
	}
}
