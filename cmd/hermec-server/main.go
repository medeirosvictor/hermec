// Command hermec-server runs a Hermec server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/medeirosvictor/hermec/server"
)

func main() {
	configPath := flag.String("config", "", "path to a TOML config file (defaults apply without it)")
	printConfig := flag.Bool("print-config", false, "print the effective config (password redacted) and exit")
	flag.Parse()

	cfg := server.DefaultConfig()
	if *configPath != "" {
		var err error
		if cfg, err = server.LoadConfig(*configPath); err != nil {
			log.Fatal(err)
		}
	}

	if *printConfig {
		shown := cfg
		if shown.Password != "" {
			shown.Password = "<redacted>"
		}
		out, err := shown.MarshalTOML()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(out))
		return
	}

	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		log.Fatal(err)
	}
	scheme := "ws"
	if cfg.TLSCert != "" {
		scheme = "wss"
	}
	log.Printf("listening on %s (%s)", displayAddr(srv.Addr()), scheme)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Print("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

// displayAddr renders a wildcard listen address as ":port".
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return ":" + port
	}
	return addr
}
