// Command hermec-verify proves a pinned public Hermec voice path between two endpoints.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/version"
)

var fingerprintRE = regexp.MustCompile(`^[a-z2-7]{4}(?:-[a-z2-7]{4}){3}$`)

// testLoopbackICEEnv is intentionally undocumented: command E2E tests set it
// for their child processes so their Pion UDP sockets cannot touch a LAN
// interface. Its absence preserves normal verifier behavior.
const testLoopbackICEEnv = "HERMEC_VERIFY_TEST_LOOPBACK_ICE"

// maxPasswordBytes bounds stdin secret handling. It is deliberately small and
// documented in dedicated-hosting.md so an untrusted pipe cannot grow memory.
const maxPasswordBytes = 256

type options struct {
	url, serverID, identity, peer, channel, diagnostics string
	mode                                                string
	timeout                                             time.Duration
	frames                                              int
	version                                             bool
}

type report struct {
	Stage            string `json:"stage"`
	Code             string `json:"code"`
	ServerID         string `json:"server_id"`
	LocalFingerprint string `json:"local_fingerprint"`
	ExpectedPeer     string `json:"expected_peer_fingerprint"`
	Success          bool   `json:"success"`
	DurationMS       int64  `json:"duration_ms"`
	PeerPresent      int    `json:"peer_present"`
	ValidFrames      int    `json:"valid_frames"`
}

func parseOptions(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("hermec-verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.mode, "mode", "endpoint", "verification mode (endpoint or init)")
	fs.StringVar(&o.url, "url", "", "trusted wss URL")
	fs.StringVar(&o.serverID, "server-id", "", "pinned server ID")
	fs.StringVar(&o.identity, "identity", "", "local identity file")
	fs.StringVar(&o.peer, "expected-peer", "", "expected peer fingerprint file")
	fs.StringVar(&o.channel, "channel", "", "voice channel")
	fs.StringVar(&o.diagnostics, "diagnostics", "", "optional safe JSON report")
	fs.DurationVar(&o.timeout, "timeout", 45*time.Second, "overall timeout (1s-5m)")
	fs.IntVar(&o.frames, "frames", 10, "required peer marker frames (1-1000)")
	fs.BoolVar(&o.version, "version", false, "print version")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	if o.version {
		return o, nil
	}
	if o.mode == "init" {
		if o.identity == "" || o.url != "" || o.serverID != "" || o.peer != "" || strings.TrimSpace(o.channel) != "" || o.diagnostics != "" {
			return o, errors.New("init mode requires only -identity")
		}
		return o, nil
	}
	if o.mode != "endpoint" {
		return o, errors.New("unsupported verification mode")
	}
	u, err := url.Parse(o.url)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return o, errors.New("-url must be a normal wss:// URL")
	}
	if o.serverID == "" || o.identity == "" || o.peer == "" || strings.TrimSpace(o.channel) == "" {
		return o, errors.New("-server-id, -identity, -expected-peer, and -channel are required")
	}
	if o.timeout < time.Second || o.timeout > 5*time.Minute || o.frames < 1 || o.frames > 1000 {
		return o, errors.New("timeout or frame threshold out of bounds")
	}
	return o, nil
}

func readPassword(ctx context.Context, r io.Reader) (string, error) {
	type result struct {
		password string
		err      error
	}
	resultC := make(chan result, 1)
	go func() {
		password, err := readPasswordLine(r)
		resultC <- result{password, err}
	}()
	select {
	case got := <-resultC:
		return got.password, got.err
	case <-ctx.Done():
		return "", errors.New("password input timed out")
	}
}

// readPasswordLine reads at most maxPasswordBytes password bytes plus an
// optional CR before newline. Reading one byte at a time keeps the bound true
// even for a Reader backed by an arbitrarily large pipe or buffer.
func readPasswordLine(r io.Reader) (string, error) {
	var line [maxPasswordBytes + 1]byte
	n := 0
	for {
		var b [1]byte
		count, err := r.Read(b[:])
		if count > 0 {
			if b[0] == '\n' {
				return finishPassword(line[:n])
			}
			if n == len(line) {
				return "", errors.New("password from stdin is too long")
			}
			line[n] = b[0]
			n++
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return finishPassword(line[:n])
			}
			return "", errors.New("cannot read password from stdin")
		}
	}
}

func finishPassword(line []byte) (string, error) {
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		return "", errors.New("password from stdin is empty")
	}
	if len(line) > maxPasswordBytes {
		return "", errors.New("password from stdin is too long")
	}
	return string(line), nil
}

func readFingerprint(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || (runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0) {
		return "", errors.New("expected peer fingerprint file is unsafe")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("cannot read expected peer fingerprint")
	}
	fp := strings.TrimSpace(string(b))
	if !fingerprintRE.MatchString(fp) {
		return "", errors.New("expected peer fingerprint is invalid")
	}
	return fp, nil
}

func marker(fp string) []byte {
	sum := sha256.Sum256([]byte("hermec-verify-marker:" + fp))
	return append([]byte{0x48, 0x56}, sum[:10]...)
}

// opusMarkerFrame is an RFC 6716 code-3, one-frame, padded Opus packet. FF FE
// is the standard 20 ms comfort-noise payload; the ignored padding carries our
// endpoint marker and sequence, so a normal Opus decoder can play every frame.
func opusMarkerFrame(mark []byte, seq uint32) []byte {
	f := []byte{0xfb, 0x41, byte(len(mark) + 4), 0xff, 0xfe}
	f = append(f, mark...)
	return append(f, byte(seq>>24), byte(seq>>16), byte(seq>>8), byte(seq))
}

func parseOpusMarker(frame []byte, want []byte) (uint32, bool) {
	if len(frame) != 5+len(want)+4 || len(frame) < 5 || frame[0] != 0xfb || frame[1] != 0x41 || int(frame[2]) != len(want)+4 || frame[3] != 0xff || frame[4] != 0xfe || !bytes.Equal(frame[5:5+len(want)], want) {
		return 0, false
	}
	i := 5 + len(want)
	return uint32(frame[i])<<24 | uint32(frame[i+1])<<16 | uint32(frame[i+2])<<8 | uint32(frame[i+3]), true
}

type markerSource struct {
	marker []byte
	seq    uint32
}

func (s *markerSource) ReadOpusFrame() ([]byte, error) {
	time.Sleep(20 * time.Millisecond)
	s.seq++
	return opusMarkerFrame(s.marker, s.seq), nil
}

type markerSink struct {
	mu              sync.Mutex
	expected, local string
	marker          []byte
	valid           int
	lastSeq         uint32
	bad             bool
	notify          chan struct{}
}

func (s *markerSink) WriteOpusFrame(from string, frame []byte) {
	s.mu.Lock()
	seq, ok := parseOpusMarker(frame, s.marker)
	if from != s.local && from != s.expected {
		s.bad = true
	} else if from == s.expected && ok && (s.valid == 0 || seq > s.lastSeq) {
		// UDP can reorder a packet; stale/replayed or ordinary non-marker audio
		// is not additional evidence, but does not invalidate a later monotonic
		// proof from the expected authenticated sender.
		s.valid++
		s.lastSeq = seq
	}
	s.mu.Unlock()
	if s.notify != nil {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
}
func (s *markerSink) result() (int, bool) { s.mu.Lock(); defer s.mu.Unlock(); return s.valid, s.bad }

func loadIdentity(path string) (*identity.Identity, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || (runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("identity file is unsafe")
	}
	return identity.Load(path)
}

func writeReport(path string, r report) error {
	if path == "" {
		return nil
	}
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
			return errors.New("diagnostics file has unsafe permissions")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect diagnostics path")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".hermec-verify-")
	if err != nil {
		return errors.New("cannot create diagnostics")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.New("cannot install diagnostics")
	}
	return nil
}

func dialFailure(err error) (string, string) {
	var serverErr *client.ServerError
	if errors.As(err, &serverErr) {
		switch serverErr.Code {
		case proto.CodeServerIDMismatch, proto.CodeBadChain, proto.CodeHostNotAuthorized:
			return safeFailure("pin")
		case proto.CodeAuthFailed, proto.CodeBanned:
			return safeFailure("admission")
		}
	}
	return safeFailure("dial")
}

func safeFailure(stage string) (string, string) {
	switch stage {
	case "dial":
		return "URL_TLS", "check URL, DNS, certificate, and system trust"
	case "pin":
		return "PIN_IDENTITY", "check the pinned server ID and local identity"
	case "admission":
		return "ADMISSION", "check admission and password supplied on stdin"
	case "peer":
		return "PEER_NOT_PRESENT", "start the separately operated expected peer in the same channel"
	case "join":
		return "NEGOTIATION_UDP", "voice negotiation did not complete; check UDP, firewall, NAT, and media configuration"
	default:
		return "AUDIO_INVALID", "check both endpoints, UDP path, and expected peer fingerprint"
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) int {
	o, err := parseOptions(args)
	if err != nil {
		fmt.Fprintln(stdout, "verification failed: INVALID_ARGUMENT")
		return 2
	}
	if o.version {
		fmt.Fprintf(stdout, "hermec-verify %s\n", version.Version)
		return 0
	}
	if o.mode == "init" {
		id, err := identity.Generate()
		if err != nil || id.Save(o.identity) != nil {
			fmt.Fprintln(stdout, "identity initialization failed")
			return 1
		}
		fmt.Fprintf(stdout, "identity initialized: %s\n", identity.Fingerprint(id.PublicKey()))
		return 0
	}
	started := time.Now()
	r := report{ServerID: o.serverID}
	stage := "identity"
	code, advice := "", ""
	finish := func(ok bool) int {
		r.Success = ok
		r.Stage = stage
		r.Code = code
		r.DurationMS = time.Since(started).Milliseconds()
		if err := writeReport(o.diagnostics, r); err != nil {
			fmt.Fprintln(stdout, "verification failed: DIAGNOSTICS")
			return 1
		}
		if ok {
			fmt.Fprintln(stdout, "verification succeeded: authenticated pinned signaling and peer marker audio")
			return 0
		}
		fmt.Fprintf(stdout, "verification failed: %s; %s\n", code, advice)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	stage = "admission"
	password, err := readPassword(ctx, stdin)
	if err != nil {
		code, advice = "ADMISSION", "supply the join password once on stdin"
		return finish(false)
	}
	peer, err := readFingerprint(o.peer)
	if err != nil {
		code, advice = "PIN_IDENTITY", "check expected peer fingerprint file"
		return finish(false)
	}
	r.ExpectedPeer = peer
	id, err := loadIdentity(o.identity)
	if err != nil {
		code, advice = "PIN_IDENTITY", "check local identity file"
		return finish(false)
	}
	r.LocalFingerprint = identity.Fingerprint(id.PublicKey())
	if r.LocalFingerprint == peer {
		code, advice = "PIN_IDENTITY", "use two distinct identities"
		return finish(false)
	}
	stage = "dial"
	c, err := client.DialPinned(ctx, o.url, id, "hermec-verify", password, o.serverID)
	password = ""
	if err != nil {
		code, advice = dialFailure(err)
		if code == "PIN_IDENTITY" {
			stage = "pin"
		} else if code == "ADMISSION" {
			stage = "admission"
		}
		return finish(false)
	}
	defer c.Close()
	if os.Getenv(testLoopbackICEEnv) == "1" {
		c.SetLoopbackICE(true)
	}
	stage = "join"
	sink := &markerSink{expected: peer, local: r.LocalFingerprint, marker: marker(peer), notify: make(chan struct{}, 1)}
	if err = c.JoinVoice(ctx, o.channel, &markerSource{marker: marker(r.LocalFingerprint)}, sink); err != nil {
		code, advice = safeFailure(stage)
		return finish(false)
	}
	// Events carry authenticated voice state; sink notifications make media
	// completion independent of subsequent signaling traffic. Keep sending
	// briefly after local evidence arrives so the independently run peer can
	// complete its reciprocal sequence proof rather than losing its source.
	var evidenceAt time.Time
	check := func() int {
		n, bad := sink.result()
		r.ValidFrames = n
		if bad {
			stage = "audio"
			code, advice = safeFailure(stage)
			return finish(false)
		}
		if r.PeerPresent == 1 && n >= o.frames {
			if evidenceAt.IsZero() {
				evidenceAt = time.Now()
			}
			if time.Since(evidenceAt) >= 500*time.Millisecond {
				stage, code = "complete", "OK"
				return finish(true)
			}
		}
		return -1
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				code, advice = safeFailure("audio")
				return finish(false)
			}
			if ev.Voice != nil && ev.Voice.Channel == o.channel {
				for _, m := range ev.Voice.Members {
					switch m.Fingerprint {
					case peer:
						r.PeerPresent = 1
					case r.LocalFingerprint:
					default:
						// A verification channel is exclusive: a third participant
						// makes its media unsuitable as two-endpoint evidence.
						stage = "audio"
						code, advice = safeFailure(stage)
						return finish(false)
					}
				}
			}
			if result := check(); result >= 0 {
				return result
			}
		case <-sink.notify:
			if result := check(); result >= 0 {
				return result
			}
		case <-tick.C:
			if result := check(); result >= 0 {
				return result
			}
		case <-ctx.Done():
			n, bad := sink.result()
			r.ValidFrames = n
			if bad || n > 0 {
				stage = "audio"
			} else if r.PeerPresent == 0 {
				stage = "peer"
			} else {
				stage = "audio"
			}
			code, advice = safeFailure(stage)
			return finish(false)
		}
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout)) }
