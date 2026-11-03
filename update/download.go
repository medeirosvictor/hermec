package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrUnsupported means this platform is updated by hand (macOS and anything
// without a release asset).
var ErrUnsupported = errors.New("update: no automatic update for this platform")

// ErrNotWritable means the OS refused (permission) to write in the folder
// holding the executable, so the user has to install the release themselves.
var ErrNotWritable = errors.New("update: install folder is not writable")

// Test seams and limits.
var (
	goos, goarch = runtime.GOOS, runtime.GOARCH
	pubKey       = PublicKey
	createTemp   = os.CreateTemp

	// allowURL decides whether a download (redirect false) or a redirect
	// target (redirect true) may be fetched.
	allowURL = allowedURL

	maxSums   int64 = 1 << 20
	maxSig    int64 = 4 << 10
	maxAsset  int64 = 256 << 20
	maxBinary int64 = 256 << 20

	metaTimeout  = 30 * time.Second
	assetTimeout = 10 * time.Minute
)

const downloadPath = "/medeirosvictor/hermec/releases/download/"

// AssetName is the release asset that carries the client for a platform.
func AssetName(goos, goarch string) (string, bool) {
	if goarch != "amd64" && goarch != "arm64" {
		return "", false
	}
	switch goos {
	case "windows":
		return "hermec-windows-" + goarch + ".zip", true
	case "linux":
		return "hermec-linux-" + goarch + ".tar.gz", true
	}
	return "", false
}

// Supported reports whether this build can update itself in place.
func Supported() bool {
	_, ok := AssetName(goos, goarch)
	return ok
}

// allowedURL permits https downloads from this project's GitHub release path
// and the hosts GitHub redirects release assets to. The first request must be
// on github.com; redirects may also go to the asset CDN. No userinfo, no port.
func allowedURL(u *url.URL, redirect bool) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Host {
	case "github.com":
		return strings.HasPrefix(u.Path, downloadPath) && !strings.Contains(u.Path, "..")
	case "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return redirect
	}
	return false
}

var httpClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("update: too many redirects")
		}
		if !allowURL(req.URL, true) {
			return fmt.Errorf("update: redirect to %s refused", req.URL.Host)
		}
		return nil
	},
}

// notWritable marks err as ErrNotWritable when the OS refused permission;
// any other failure is returned as it is.
func notWritable(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: %v", ErrNotWritable, err)
	}
	return err
}

// Download fetches the release's artifact for this platform, verifies it and
// stages the new binary next to the running executable as <exe>.new. It
// installs nothing unless the SHA256SUMS.txt signature verifies under
// PublicKey for a.Version (the tag is part of the signed bytes) and the
// artifact's SHA-256 is listed in that file. a.Version must also be newer
// than current, so a stale release cannot be replayed as an update. It returns
// the SHA-256 of the extracted binary, which Apply checks again before the
// swap. The binary is written to a temp file in the install folder and only
// renamed to <exe>.new once it is complete; any failure leaves no staged file
// behind. One update lock per install keeps two instances from interleaving.
func Download(ctx context.Context, a Available, current string) (sum [32]byte, err error) {
	name, ok := AssetName(goos, goarch)
	if !ok {
		return sum, ErrUnsupported
	}
	if _, ok := parse(a.Version); !ok {
		return sum, fmt.Errorf("update: bad release tag %q", a.Version)
	}
	if !Newer(a.Version, current) {
		return sum, fmt.Errorf("update: %s is not newer than %s", a.Version, current)
	}
	urls := map[string]string{}
	for _, n := range []string{name, SumsName, SigName} {
		raw, err := assetURL(a, n)
		if err != nil {
			return sum, err
		}
		urls[n] = raw
	}

	// Find out whether we can install before fetching anything.
	exe, err := installPath()
	if err != nil {
		return sum, err
	}
	lock, err := lockFile(lockPath(exe))
	if err != nil {
		return sum, notWritable(err)
	}
	defer lock.Close()
	part, err := createTemp(filepath.Dir(exe), ".hermec-*.part")
	if err != nil {
		return sum, notWritable(err)
	}
	renamed := false
	defer func() {
		part.Close()
		if !renamed {
			os.Remove(part.Name())
		}
	}()

	sums, err := fetch(ctx, urls[SumsName], maxSums, metaTimeout)
	if err != nil {
		return sum, err
	}
	sig, err := fetch(ctx, urls[SigName], maxSig, metaTimeout)
	if err != nil {
		return sum, err
	}
	if err := VerifySums(pubKey, a.Version, sums, sig); err != nil {
		return sum, err
	}
	listed, err := ParseSums(sums)
	if err != nil {
		return sum, err
	}
	want, ok := listed[name]
	if !ok {
		return sum, fmt.Errorf("update: %s is not in the signed checksums", name)
	}

	tmp, err := os.CreateTemp("", "hermec-update-*")
	if err != nil {
		return sum, err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	h := sha256.New()
	if err := fetchTo(ctx, io.MultiWriter(tmp, h), urls[name], maxAsset, assetTimeout); err != nil {
		return sum, err
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != want {
		return sum, fmt.Errorf("update: %s does not match its signed checksum", name)
	}

	// Extract from the handle that was hashed, not from a path that could
	// have been swapped since.
	size, err := tmp.Seek(0, io.SeekEnd)
	if err != nil {
		return sum, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return sum, err
	}
	bin := "hermec"
	if goos == "windows" {
		bin = "hermec.exe"
	}
	bh := sha256.New()
	w := io.MultiWriter(part, bh)
	if strings.HasSuffix(name, ".zip") {
		err = extractZip(tmp, size, bin, w)
	} else {
		err = extractTarGz(tmp, bin, w)
	}
	if err != nil {
		return sum, err
	}
	if err := part.Chmod(0o755); err != nil {
		return sum, err
	}
	if err := part.Sync(); err != nil {
		return sum, err
	}
	if err := part.Close(); err != nil {
		return sum, err
	}
	// Complete: only now does it get the name Apply looks for.
	staged := exe + ".new"
	os.Remove(staged)
	if err := rename(part.Name(), staged); err != nil {
		return sum, notWritable(err)
	}
	renamed = true
	copy(sum[:], bh.Sum(nil))
	return sum, nil
}

// assetURL is the validated download URL of the named asset of a.
func assetURL(a Available, name string) (string, error) {
	raw := a.Assets[name]
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		return "", fmt.Errorf("update: release has no usable %s", name)
	}
	// The path must be exactly this release's asset: a signed checksums file
	// from another release must not be served as this one's. (The signature
	// also binds the tag; this is the second layer.)
	if !allowURL(u, false) || u.Path != downloadPath+a.Version+"/"+name ||
		u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("update: refusing download URL for %s", name)
	}
	return raw, nil
}

func fetch(ctx context.Context, raw string, max int64, timeout time.Duration) ([]byte, error) {
	var b strings.Builder
	if err := fetchTo(ctx, &b, raw, max, timeout); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// fetchTo copies a 200 response of at most max bytes into w.
func fetchTo(ctx context.Context, w io.Writer, raw string, max int64, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return errors.New("update: download too large")
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return errors.New("update: download too large")
	}
	return nil
}
