package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/thesyncim/gopus"
)

func TestParseOptionsRequiresPinnedWSS(t *testing.T) {
	for _, args := range [][]string{{}, {"-url", "ws://example.test", "-server-id", "x", "-identity", "i", "-expected-peer", "p", "-channel", "v"}, {"-url", "wss://example.test/?secret=value", "-server-id", "x", "-identity", "i", "-expected-peer", "p", "-channel", "v"}, {"-url", "wss://example.test", "-identity", "i", "-expected-peer", "p", "-channel", "v"}} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%q) succeeded", args)
		}
	}
	if _, err := parseOptions([]string{"-url", "wss://example.test/", "-server-id", "x", "-identity", "i", "-expected-peer", "p", "-channel", "v", "-timeout", "1s", "-frames", "1"}); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordStdinOnlyAndNotDisclosed(t *testing.T) {
	got, err := readPassword(context.Background(), strings.NewReader("very-secret\nignored"))
	if err != nil || got != "very-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
	var out bytes.Buffer
	if run([]string{"-url", "not-a-url"}, strings.NewReader("very-secret\n"), &out) != 2 || strings.Contains(out.String(), "very-secret") {
		t.Fatalf("unsafe output %q", out.String())
	}
}

func TestCommandAdmissionFailures(t *testing.T) {
	d := t.TempDir()
	identityPath := filepath.Join(d, "identity")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(identityPath); err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(d, "peer")
	if err := os.WriteFile(peerPath, []byte("abcd-efgh-ijkl-mnop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"-url", "wss://127.0.0.1:1/", "-server-id", "server", "-identity", identityPath, "-expected-peer", peerPath, "-channel", "verify", "-timeout", "1s"}
	var out bytes.Buffer
	if got := run(base, strings.NewReader("\n"), &out); got != 1 || !strings.Contains(out.String(), "ADMISSION") {
		t.Fatalf("empty-stdin admission = %d, %q", got, out.String())
	}

}

func TestRunRejectsUntrustedTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TLS listener test is disabled on Windows to avoid Firewall prompts")
	}

	// The command must use normal system TLS verification: this live TLS
	// endpoint has an intentionally untrusted httptest certificate.
	tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsServer.Close()
	host, _, err := net.SplitHostPort(strings.TrimPrefix(tlsServer.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	assertIPv4Loopback(t, host)

	d := t.TempDir()
	identityPath := filepath.Join(d, "identity")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(identityPath); err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(d, "peer")
	if err := os.WriteFile(peerPath, []byte("abcd-efgh-ijkl-mnop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-url", "wss" + strings.TrimPrefix(tlsServer.URL, "https"), "-server-id", "server", "-identity", identityPath, "-expected-peer", peerPath, "-channel", "verify", "-timeout", "1s"}
	var out bytes.Buffer
	if got := run(args, strings.NewReader("password\n"), &out); got != 1 || !strings.Contains(out.String(), "URL_TLS") {
		t.Fatalf("untrusted TLS endpoint = %d, %q", got, out.String())
	}
}

func TestIdentityInitIsExclusiveAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endpoint.identity")
	var out bytes.Buffer
	if got := run([]string{"-mode", "init", "-identity", path}, strings.NewReader(""), &out); got != 0 || !strings.Contains(out.String(), "identity initialized: ") {
		t.Fatalf("init = %d, %q", got, out.String())
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if got := run([]string{"-mode", "init", "-identity", path}, strings.NewReader(""), &out); got != 1 {
		t.Fatalf("second init = %d", got)
	}
}

func TestOversizedUnterminatedPasswordIsNeverDisclosed(t *testing.T) {
	const helperEnv = "HERMEC_VERIFY_PASSWORD_SENTINEL_HELPER"
	if os.Getenv(helperEnv) == "1" {
		var args []string
		if err := json.Unmarshal([]byte(os.Getenv(helperEnv+"_ARGS")), &args); err != nil {
			os.Exit(2)
		}
		os.Exit(run(args, os.Stdin, os.Stdout))
	}

	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(identityPath); err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(dir, "peer")
	if err := os.WriteFile(peerPath, []byte("abcd-efgh-ijkl-mnop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "report.json")
	args := []string{"-url", "wss://127.0.0.1:1/", "-server-id", "server", "-identity", identityPath, "-expected-peer", peerPath, "-channel", "verify", "-timeout", "1s", "-diagnostics", reportPath}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}

	// This has neither a newline nor EOF until the child consumes all bytes.
	// Keep the sentinel distinct so leakage checks cover every output surface.
	const sentinelSeed = "oversized-unterminated-password-sentinel-"
	sentinel := strings.Repeat(sentinelSeed, (maxPasswordBytes+len(sentinelSeed))/len(sentinelSeed))[:maxPasswordBytes+1]
	// Exactly one byte over the limit makes readPasswordLine consume through EOF
	// before rejecting this unterminated input.
	if len(sentinel) != maxPasswordBytes+1 {
		t.Fatal("test sentinel has the wrong size")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestOversizedUnterminatedPasswordIsNeverDisclosed$")
	cmd.Env = append(os.Environ(), helperEnv+"=1", helperEnv+"_ARGS="+string(encodedArgs))
	cmd.Stdin = strings.NewReader(sentinel)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("oversized password was accepted")
	}
	if !strings.Contains(stdout.String(), "ADMISSION") {
		t.Fatal("verifier output missing ADMISSION code")
	}
	rawReport, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	var diagnostics report
	if err := json.Unmarshal(rawReport, &diagnostics); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if diagnostics.Stage != "admission" || diagnostics.Code != "ADMISSION" {
		t.Fatal("diagnostics missing admission ADMISSION status")
	}
	const sentinelFragment = "unterminated-password-sentinel"
	for _, output := range map[string]string{
		"stdout":      stdout.String(),
		"stderr":      stderr.String(),
		"diagnostics": string(rawReport),
	} {
		if strings.Contains(output, sentinelFragment) {
			t.Fatal("password sentinel fragment leaked")
		}
		if strings.Contains(output, sentinel) {
			t.Fatal("password sentinel leaked")
		}
	}
}

func TestPasswordInputIsByteBounded(t *testing.T) {
	tooLong := strings.Repeat("s", maxPasswordBytes+1)
	for _, input := range []string{tooLong + "\n", tooLong} {
		got, err := readPassword(context.Background(), strings.NewReader(input))
		if err == nil || got != "" || strings.Contains(err.Error(), tooLong) {
			t.Fatalf("over-limit password result = %q, %v", got, err)
		}
	}

	password := strings.Repeat("s", maxPasswordBytes)
	if got, err := readPassword(context.Background(), strings.NewReader(password+"\r\n")); err != nil || got != password {
		t.Fatalf("maximum password result = %q, %v", got, err)
	}
}

func TestPasswordInputHonorsTimeoutBeforeNewlineOrEOF(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := readPassword(ctx, r); err == nil || time.Since(started) > time.Second {
		t.Fatalf("password read did not honor deadline: %v", err)
	}
	// Cancellation closes real stdin (an *os.File), so the reader goroutine has
	// exited instead of retaining its partial password buffer.
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("timed out password input was not closed")
	}
}

func TestFingerprintAndMarkers(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "peer")
	if err := os.WriteFile(p, []byte("abcd-efgh-ijkl-mnop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp, err := readFingerprint(p)
	if err != nil || fp != "abcd-efgh-ijkl-mnop" {
		t.Fatalf("%q %v", fp, err)
	}
	if err := os.WriteFile(p, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFingerprint(p); err == nil {
		t.Fatal("accepted invalid fingerprint")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readFingerprint(p); err == nil {
			t.Fatal("accepted exposed fingerprint file")
		}
		if err := os.Chmod(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	frame := opusMarkerFrame(marker("peer"), 7)
	if seq, ok := parseOpusMarker(frame, marker("peer")); !ok || seq != 7 {
		t.Fatalf("invalid Opus marker frame: seq=%d ok=%v", seq, ok)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := dec.DecodeInt16(frame, make([]int16, 5760)); err != nil || n != 960 {
		t.Fatalf("marker packet is not decodable Opus: n=%d err=%v", n, err)
	}
	s := &markerSink{expected: "peer", local: "self", marker: marker("peer")}
	s.WriteOpusFrame("peer", frame)
	s.WriteOpusFrame("peer", frame) // replayed sequence is not valid evidence.
	n, bad := s.result()
	if n != 1 || bad {
		t.Fatalf("marker validation n=%d bad=%v", n, bad)
	}
}

func TestLoadIdentityRejectsUnsafeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadIdentity(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(filepath.Dir(path), "identity-link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if _, err := loadIdentity(link); err == nil {
			t.Fatal("accepted identity symlink")
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadIdentity(path); err == nil {
			t.Fatal("accepted world-readable identity")
		}
	}
}

func TestSafeDiagnostics(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.json")
	r := report{Stage: "audio", Code: "AUDIO_INVALID", Success: false, DurationMS: 1, ServerID: "id", LocalFingerprint: "a", ExpectedPeer: "b"}
	if err := writeReport(p, r); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	var got report
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil || got.Code != "AUDIO_INVALID" {
		t.Fatalf("report %s: %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeReport(p, r); err == nil {
			t.Fatal("replaced unsafe report")
		}
	}
}

func TestBoundedOptionsAndSafeFailures(t *testing.T) {
	_, err := parseOptions([]string{"-url", "wss://x", "-server-id", "s", "-identity", "i", "-expected-peer", "p", "-channel", "v", "-timeout", "0s"})
	if err == nil {
		t.Fatal("accepted zero timeout")
	}
	for _, stage := range []string{"dial", "pin", "admission", "peer", "join", "audio"} {
		code, msg := safeFailure(stage)
		if code == "" || msg == "" || strings.Contains(strings.ToLower(msg), "sdp") || strings.Contains(strings.ToLower(msg), "ice candidate") {
			t.Fatalf("unsafe failure %q %q", code, msg)
		}
	}
	for _, refusal := range []string{proto.CodeServerIDMismatch, proto.CodeBadChain, proto.CodeHostNotAuthorized} {
		if code, _ := dialFailure(&client.ServerError{Code: refusal}); code != "PIN_IDENTITY" {
			t.Fatalf("pin refusal %q mapped to %q", refusal, code)
		}
	}
	_ = time.Second
}
