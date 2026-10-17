package ui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// wsaEADDRINUSE is WSAEADDRINUSE; on Windows it is not syscall.EADDRINUSE.
const wsaEADDRINUSE = syscall.Errno(10048)

// hostedConfig builds the config for a server hosted from inside the app.
func hostedConfig(name, password string, port int, adminFP string) server.Config {
	cfg := server.DefaultConfig()
	cfg.Addr = fmt.Sprintf(":%d", port)
	cfg.ServerName = state.CleanName(name, state.MaxServerNameRunes)
	cfg.Password = password
	cfg.VoiceChannels = []string{"voice"}
	cfg.Roles.Grants = map[string][]string{adminFP: {"admin"}}
	return cfg
}

// saveHosted writes cfg atomically; the file holds the password, so 0600.
func saveHosted(path string, cfg server.Config) error {
	b, err := cfg.MarshalTOML()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hosted-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// loadHosted reads the hosted config; a missing file is ok=false, err=nil.
func loadHosted(path string) (cfg server.Config, ok bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return server.Config{}, false, nil
	}
	cfg, err = server.LoadConfig(path)
	if err != nil {
		return server.Config{}, false, err
	}
	return cfg, true, nil
}

// hostedPort parses the port from cfg.Addr, or 0 when it has none.
func hostedPort(cfg server.Config) int {
	_, p, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}

// isPortInUse reports whether err means the listen port is already taken.
func isPortInUse(err error) bool {
	if errors.Is(err, server.ErrDiscoveryBind) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == wsaEADDRINUSE || errno == syscall.EADDRINUSE
	}
	return false
}

func portInUseMsg(port int) string {
	return fmt.Sprintf("port %d is in use (is hermec-server running?)", port)
}

// inviteAddress is the address to give friends, preferring Tailscale.
func inviteAddress(tsIP, lanIP string, port int) string {
	switch {
	case tsIP != "":
		return fmt.Sprintf("ws://%s:%d/", tsIP, port)
	case lanIP != "":
		return fmt.Sprintf("ws://%s:%d/", lanIP, port)
	}
	return "no network address found"
}
