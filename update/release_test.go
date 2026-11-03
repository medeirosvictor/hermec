package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	r.files[SigName] = Sign(priv, sums)

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
	if err := Download(context.Background(), r.a); err != nil {
		t.Fatal(err)
	}
	if got := read(r.exe + ".new"); got != "NEW-BINARY" {
		t.Fatalf("staged = %q", got)
	}
	if read(r.exe) != "OLD-BINARY" {
		t.Fatal("download must not touch the running exe")
	}
}

func TestDownloadRefusals(t *testing.T) {
	cases := map[string]func(r *rel){
		"placeholder key": func(r *rel) { pubKey = PublicKey },
		"bad signature": func(r *rel) {
			r.files[SigName] = Sign(ed25519.NewKeyFromSeed(make([]byte, 32)), r.files[SumsName])
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
			r.files[SigName] = Sign(priv, r.files[SumsName])
		},
		"missing sig asset":       func(r *rel) { delete(r.a.Assets, SigName) },
		"url outside allowlist":   func(r *rel) { allowURL = func(*url.URL, bool) bool { return false } },
		"url for another release": func(r *rel) { r.a.Assets[SumsName] = strings.Replace(r.a.Assets[SumsName], relTag, "v1.0.0", 1) },
		"oversized sums":          func(r *rel) { maxSums = 4 },
		"oversized asset":         func(r *rel) { maxAsset = 10 },
		"unsupported platform":    func(r *rel) { goos = "darwin" },
		"bad tag":                 func(r *rel) { r.a.Version = "latest" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRel(t)
			mod(r)
			if err := Download(context.Background(), r.a); err == nil {
				t.Fatal("accepted")
			}
			r.assertNothingStaged()
		})
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
	if err := Download(context.Background(), r.a); err == nil {
		t.Fatal("redirect to a host outside the allowlist was followed")
	}
	r.assertNothingStaged()
}

func TestDownloadUnwritableFolder(t *testing.T) {
	r := newRel(t)
	// A non-empty directory where the staged file belongs cannot be removed or
	// replaced on any OS, standing in for a read-only install folder.
	if err := os.MkdirAll(filepath.Join(r.exe+".new", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), r.a); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("err = %v, want ErrNotWritable", err)
	}
}

func TestDownloadCancelled(t *testing.T) {
	r := newRel(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Download(ctx, r.a); err == nil {
		t.Fatal("cancelled download succeeded")
	}
	r.assertNothingStaged()
}
