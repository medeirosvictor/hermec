package main

import (
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/medeirosvictor/hermec/server"
)

// knownHostsFile is the file next to the config where the host remembers the
// other hosts it has learned, one URL per line.
const knownHostsFile = "known_hosts.txt"

// flags are the command line options.
type flags struct {
	config, key, chain, history string
	printConfig, applyConfig    bool
	advertise, hosts            []string
	name                        string
}

// parseFlags parses the command line (without the program name).
func parseFlags(args []string) (flags, error) {
	var f flags
	var advertise, hosts string
	fs := flag.NewFlagSet("hermec-server", flag.ContinueOnError)
	fs.StringVar(&f.config, "config", "", "path to a TOML config file (defaults apply without it)")
	fs.BoolVar(&f.printConfig, "print-config", false, "print the effective config (password redacted) and exit")
	fs.StringVar(&f.key, "key", "", "host key file (default hermec-server.key next to -config; generated when missing)")
	fs.StringVar(&f.chain, "chain", "", "server chain file (default chain.jsonl next to -config)")
	fs.StringVar(&f.history, "history", "", "chat history file (default history.jsonl next to -config)")
	fs.BoolVar(&f.applyConfig, "apply-config", false, "append the config file's group fields to the chain, print the count, and exit; stop the server first, the changes apply when it starts")
	fs.StringVar(&advertise, "advertise", "", "comma list of ws URLs clients and hosts reach this host at (default: ws://<non-loopback IPs>:<port>/)")
	fs.StringVar(&hosts, "hosts", "", "comma list of ws URLs of other hosts of this server (merged with known_hosts.txt next to -config)")
	fs.StringVar(&f.name, "name", "", "host display name (default: the machine's hostname)")
	if err := fs.Parse(args); err != nil {
		return flags{}, err
	}
	f.advertise = cleanURLs(splitTrim(advertise, ","), "-advertise")
	f.hosts = cleanURLs(splitTrim(hosts, ","), "-hosts")
	return f, nil
}

// splitTrim splits s at sep, trimming blanks and dropping empty items.
func splitTrim(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildHostConfig turns cfg and the host flags into the config of a
// server.Host. dir holds known_hosts.txt. notify gets the role lines.
func buildHostConfig(f flags, cfg server.Config, dir string, notify func(string)) server.HostConfig {
	kh := &knownHosts{path: filepath.Join(dir, knownHostsFile), flagged: f.hosts, configured: cleanURLs(cfg.KnownHosts, "known_hosts")}
	cfg.Advertise = f.advertise
	if len(cfg.Advertise) == 0 {
		ips, err := localIPs()
		if err != nil {
			log.Printf("WARNING: listing interface addresses: %v", err)
		}
		cfg.Advertise = defaultAdvertise(cfg.Addr, cfg.TLSCert != "", ips)
	}
	cfg.HostName = f.name
	if cfg.HostName == "" {
		cfg.HostName, _ = os.Hostname()
	}
	return server.HostConfig{Server: cfg, Known: kh.list, SaveKnown: kh.save, Notify: notify}
}

// localIPs lists the addresses of the machine's interfaces.
func localIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out = append(out, n.IP)
		}
	}
	return out, err
}

// defaultAdvertise is the URL list for a host listening on addr: the bound
// host when addr names one, else one URL per global or private unicast IP in
// ips. A port of 0 or none yields nothing, as the real port is unknown.
func defaultAdvertise(addr string, tls bool, ips []net.IP) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		return nil
	}
	scheme := "ws"
	if tls {
		scheme = "wss"
	}
	url := func(h string) string { return scheme + "://" + net.JoinHostPort(h, port) + "/" }
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		return []string{url(host)}
	}
	var out []string
	for _, ip := range ips {
		if ip.IsGlobalUnicast() {
			out = append(out, url(ip.String()))
		}
	}
	return out
}

// knownHosts merges the host URLs from -hosts, the config and known_hosts.txt.
type knownHosts struct {
	path       string
	flagged    []string
	configured []string
	mu         sync.Mutex
	saveFailed bool // the save error was logged
}

// warned holds the bad entries already reported, so repeated reads of
// known_hosts.txt warn once.
var warned sync.Map

// cleanURLs keeps the entries of items that are ws or wss URLs with a host,
// gives each an explicit path (ws://a:1 becomes ws://a:1/) and drops
// duplicates. It warns once about each entry it skips; what names the source.
func cleanURLs(items []string, what string) []string {
	var out []string
	for _, s := range items {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			if _, dup := warned.LoadOrStore(what+" "+s, true); !dup {
				log.Printf("WARNING: %s: ignoring %q (want a ws:// or wss:// URL with a host)", what, s)
			}
			continue
		}
		if u.Path == "" {
			u.Path = "/"
		}
		if n := u.String(); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// fileURLs reads known_hosts.txt; a missing file is empty.
func (k *knownHosts) fileURLs() []string {
	b, err := os.ReadFile(k.path)
	if err != nil {
		return nil
	}
	return cleanURLs(splitTrim(string(b), "\n"), knownHostsFile)
}

// list is the merged set, flags first.
func (k *knownHosts) list() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.merged(k.fileURLs())
}

func (k *knownHosts) merged(file []string) []string {
	var out []string
	for _, l := range [][]string{k.flagged, k.configured, file} {
		for _, u := range l {
			if !slices.Contains(out, u) {
				out = append(out, u)
			}
		}
	}
	return out
}

// save adds urls to known_hosts.txt (0600). Failures are not fatal: the host
// relearns them from its peers.
func (k *knownHosts) save(urls []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	file := k.fileURLs()
	for _, u := range cleanURLs(urls, "learned host") {
		if !slices.Contains(file, u) {
			file = append(file, u)
		}
	}
	if err := writeFile0600(k.path, []byte(strings.Join(file, "\n")+"\n")); err != nil && !k.saveFailed {
		k.saveFailed = true
		log.Printf("WARNING: saving %s: %v", k.path, err)
	}
}

// writeFile0600 replaces path through a temp file so a crash leaves the old one.
func writeFile0600(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
