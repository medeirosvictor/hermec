// Command hermec-release holds the release-signing tools: keygen makes the
// Ed25519 key pair, sign signs a SHA256SUMS.txt, verify checks one. See
// docs/release-signing.md.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/medeirosvictor/hermec/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hermec-release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hermec-release keygen -out <path> | sign -tag <vX.Y.Z> -key-env <VAR> <sums> | verify -tag <vX.Y.Z> [-pub <base64>] <sums>")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:])
	case "sign":
		return sign(args[1:])
	case "verify":
		return verify(args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// keygen writes the base64 private seed to -out (mode 0600, never replacing a
// file) and prints the base64 public key.
func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "path to write the private key to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() != 0 {
		return errors.New("usage: hermec-release keygen -out <path>")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(*out)
		return werr
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

// checkTag rejects a -tag value that cannot be bound into a signature: empty,
// or holding a NUL or a line break.
func checkTag(tag string) error {
	if tag == "" || strings.ContainsAny(tag, "\x00\r\n") {
		return errors.New("-tag is required and must be a release tag such as v1.2.3")
	}
	return nil
}

// sign reads the base64 seed from the environment variable named by -key-env
// and writes <sums>.sig next to the file. The signature covers the release tag.
func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyEnv := fs.String("key-env", "", "environment variable holding the base64 private seed")
	tag := fs.String("tag", "", "release tag the signature is bound to (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyEnv == "" || fs.NArg() != 1 {
		return errors.New("usage: hermec-release sign -tag <vX.Y.Z> -key-env <VAR> <sums>")
	}
	if err := checkTag(*tag); err != nil {
		return err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(*keyEnv)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("$%s is unset or not a base64 Ed25519 seed", *keyEnv)
	}
	sums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(0)+".sig", update.Sign(ed25519.NewKeyFromSeed(seed), *tag, sums), 0o644)
}

// verify checks <sums>.sig for release -tag against the base64 public key, by
// default the one compiled into the app, and prints the tag that was signed.
func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pub := fs.String("pub", update.PublicKey, "base64 public key (default: the key compiled into the app)")
	tag := fs.String("tag", "", "release tag the signature must be bound to (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hermec-release verify -tag <vX.Y.Z> [-pub <base64>] <sums>")
	}
	if err := checkTag(*tag); err != nil {
		return err
	}
	sums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(fs.Arg(0) + ".sig")
	if err != nil {
		return err
	}
	if err := update.VerifySums(*pub, *tag, sums, sig); err != nil {
		return err
	}
	fmt.Println("signature ok for tag " + *tag)
	return nil
}
