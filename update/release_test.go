package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rel struct {
	t     *testing.T
	srv   *httptest.Server
	files map[string][]byte // asset name -> body
	exe   string
	a     Available
}

const relTag = "v2.0.0"
const relCur = "v1.0.0" // the running build
const relPrefix = "/medeirosvictor/hermec/releases/download/" + relTag + "/"

// newRel serves a release signed by the test key and points the updater at a
// fake executable in a temp dir. The platform is windows/amd64.
func newRel(t *testing.T) *rel {
	t.Helper()
	priv, pub := testKey(t)
	r := &rel{t: t, files: map[string][]byte{}}
	name := "hermec-windows-amd64.zip"
	asset := zipOf(t, map[string]string{"hermec.exe": "NEW-BINARY", "README.txt": "r"}, "hermec.exe", "README.txt")
	sum := sha256.Sum256(asset)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	r.files[name] = asset
	r.files[SumsName] = sums
	r.files[SigName] = Sign(priv, relTag, sums)

	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, relPrefix)]
		if !strings.HasPrefix(req.URL.Path, relPrefix) || !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(r.srv.Close)

	r.a = Available{Version: relTag, Assets: map[string]string{}}
	for n := range r.files {
		r.a.Assets[n] = r.srv.URL + relPrefix + n
	}
	r.exe = filepath.Join(t.TempDir(), "hermec.exe")
	if err := os.WriteFile(r.exe, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldGoos, oldGoarch, oldKey, oldExe, oldAllow := goos, goarch, pubKey, executable, allowURL
	oldSums, oldAsset := maxSums, maxAsset
	goos, goarch, pubKey = "windows", "amd64", pub
	executable = func() (string, error) { return r.exe, nil }
	host := strings.TrimPrefix(r.srv.URL, "http://")
	allowURL = func(u *url.URL, _ bool) bool { return u.Host == host }
	t.Cleanup(func() {
		goos, goarch, pubKey, executable, allowURL = oldGoos, oldGoarch, oldKey, oldExe, oldAllow
		maxSums, maxAsset = oldSums, oldAsset
	})
	return r
}

func (r *rel) assertNothingStaged() {
	r.t.Helper()
	if _, err := os.Stat(r.exe + ".new"); err == nil {
		r.t.Error("a staged file was left behind")
	}
	if read(r.exe) != "OLD-BINARY" {
		r.t.Errorf("running exe changed: %q", read(r.exe))
	}
}

func TestDownloadStages(t *testing.T) {
	r := newRel(t)
	sum, err := Download(context.Background(), r.a, relCur)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(r.exe + ".new"); got != "NEW-BINARY" {
		t.Fatalf("staged = %q", got)
	}
	if sum != sha256.Sum256([]byte("NEW-BINARY")) {
		t.Fatalf("Download returned %x, not the hash of the extracted binary", sum)
	}
	if read(r.exe) != "OLD-BINARY" {
		t.Fatal("download must not touch the running exe")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(r.exe), ".hermec-*.part")); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
	// The hash it returned is what Apply wants.
	if _, err := Apply(sum); err != nil {
		t.Fatal(err)
	}
	if read(r.exe) != "NEW-BINARY" {
		t.Fatalf("exe = %q", read(r.exe))
	}
}

func TestDownloadRefusals(t *testing.T) {
	cases := map[string]func(r *rel){
		"placeholder key": func(r *rel) { pubKey = PublicKey },
		"bad signature": func(r *rel) {
			r.files[SigName] = Sign(ed25519.NewKeyFromSeed(make([]byte, 32)), relTag, r.files[SumsName])
		},
		"tampered sums": func(r *rel) {
			r.files[SumsName] = append(r.files[SumsName], []byte(strings.Repeat("0", 64)+"  x\n")...)
		},
		"tampered asset": func(r *rel) {
			r.files["hermec-windows-amd64.zip"] = zipOf(r.t, map[string]string{"hermec.exe": "EVIL"}, "hermec.exe")
		},
		"asset not listed": func(r *rel) {
			priv, _ := testKey(r.t)
			r.files[SumsName] = []byte(strings.Repeat("0", 64) + "  other.zip\n")
			r.files[SigName] = Sign(priv, relTag, r.files[SumsName])
		},
		"missing sig asset":       func(r *rel) { delete(r.a.Assets, SigName) },
		"url outside allowlist":   func(r *rel) { allowURL = func(*url.URL, bool) bool { return false } },
		"url for another release": func(r *rel) { r.a.Assets[SumsName] = strings.Replace(r.a.Assets[SumsName], relTag, "v1.0.0", 1) },
		"oversized sums":          func(r *rel) { maxSums = 4 },
		"oversized asset":         func(r *rel) { maxAsset = 10 },
		"unsupported platform":    func(r *rel) { goos = "darwin" },
		"bad tag":                 func(r *rel) { r.a.Version = "latest" },
		"nul in tag":              func(r *rel) { r.a.Version = relTag + "\x00" },
		// A validly signed older release served under the new release's URLs:
		// its signature is bound to v1.9.0, not v2.0.0.
		"signature for another tag": func(r *rel) {
			priv, _ := testKey(r.t)
			r.files[SigName] = Sign(priv, "v1.9.0", r.files[SumsName])
		},
		"v1 domain signature": func(r *rel) {
			priv, _ := testKey(r.t)
			bare := ed25519.Sign(priv, append([]byte("hermec-release-sums-v1\x00"), r.files[SumsName]...))
			r.files[SigName] = []byte(base64.StdEncoding.EncodeToString(bare))
		},
		"query on url":    func(r *rel) { r.a.Assets[SumsName] += "?x=1" },
		"fragment on url": func(r *rel) { r.a.Assets[SumsName] += "#x" },
		"encoded path": func(r *rel) {
			r.a.Assets[SumsName] = strings.Replace(r.a.Assets[SumsName], "/download/", "/%64ownload/", 1)
		},
		"suffix-only url": func(r *rel) {
			r.a.Assets[SumsName] = strings.Replace(r.a.Assets[SumsName], "/medeirosvictor/", "/medeirosvictor/x/../", 1)
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRel(t)
			mod(r)
			if _, err := Download(context.Background(), r.a, relCur); err == nil {
				t.Fatal("accepted")
			}
			r.assertNothingStaged()
		})
	}
}

// The release must be strictly newer than the running build: the same or an
// older release, even validly signed, is never staged.
func TestDownloadRefusesNotNewer(t *testing.T) {
	for _, cur := range []string{relTag, "v2.0.1", "v3.0.0", "dev", ""} {
		r := newRel(t)
		if _, err := Download(context.Background(), r.a, cur); err == nil {
			t.Errorf("current %q: accepted", cur)
		}
		r.assertNothingStaged()
	}
}

func TestDownloadRefusesOffListRedirect(t *testing.T) {
	r := newRel(t)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("x")) }))
	defer evil.Close()
	inner := r.srv.Config.Handler
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, SumsName) {
			http.Redirect(w, req, evil.URL+"/x", http.StatusFound)
			return
		}
		inner.ServeHTTP(w, req)
	})
	if _, err := Download(context.Background(), r.a, relCur); err == nil {
		t.Fatal("redirect to a host outside the allowlist was followed")
	}
	r.assertNothingStaged()
}

// A permission error creating the staging file is ErrNotWritable, and is found
// out before any request is made.
func TestDownloadUnwritableFolder(t *testing.T) {
	r := newRel(t)
	hits := 0
	inner := r.srv.Config.Handler
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		inner.ServeHTTP(w, req)
	})
	old := createTemp
	defer func() { createTemp = old }()
	createTemp = func(string, string) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: "x", Err: fs.ErrPermission}
	}
	if _, err := Download(context.Background(), r.a, relCur); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("err = %v, want ErrNotWritable", err)
	}
	if hits != 0 {
		t.Fatalf("%d requests made before the folder was found unwritable", hits)
	}
	// Other failures are not "not writable".
	createTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk on fire") }
	if _, err := Download(context.Background(), r.a, relCur); err == nil || errors.Is(err, ErrNotWritable) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

// A non-empty directory where the staged file belongs cannot be replaced on any
// OS: the finished download is not staged, and nothing is left behind.
func TestDownloadCannotStage(t *testing.T) {
	r := newRel(t)
	if err := os.MkdirAll(filepath.Join(r.exe+".new", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), r.a, relCur); err == nil {
		t.Fatal("staged over a directory")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(r.exe), ".hermec-*.part")); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
}

// Only one instance downloads at a time.
func TestDownloadBusy(t *testing.T) {
	r := newRel(t)
	l, err := lockFile(lockPath(r.exe))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := Download(context.Background(), r.a, relCur); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	r.assertNothingStaged()
}

// The staged file appears under its final name only once it is complete: a
// download that fails during extraction never shows a .new.
func TestDownloadNeverStagesAPartialFile(t *testing.T) {
	r := newRel(t)
	name := "hermec-windows-amd64.zip"
	r.files[name] = zipOf(t, map[string]string{"hermec.exe": "B", "../evil": "x"}, "hermec.exe", "../evil")
	sum := sha256.Sum256(r.files[name])
	priv, _ := testKey(t)
	r.files[SumsName] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	r.files[SigName] = Sign(priv, relTag, r.files[SumsName])
	if _, err := Download(context.Background(), r.a, relCur); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	r.assertNothingStaged()
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(r.exe), ".hermec-*.part")); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
}

func TestDownloadCancelled(t *testing.T) {
	r := newRel(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Download(ctx, r.a, relCur); err == nil {
		t.Fatal("cancelled download succeeded")
	}
	r.assertNothingStaged()
}
