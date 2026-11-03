package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/url"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := []struct {
		os, arch, want string
	}{
		{"windows", "amd64", "hermec-windows-amd64.zip"},
		{"windows", "arm64", "hermec-windows-arm64.zip"},
		{"linux", "amd64", "hermec-linux-amd64.tar.gz"},
		{"linux", "arm64", "hermec-linux-arm64.tar.gz"},
		{"darwin", "arm64", ""},
		{"darwin", "amd64", ""},
		{"freebsd", "amd64", ""},
		{"linux", "386", ""},
	}
	for _, c := range cases {
		got, ok := AssetName(c.os, c.arch)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("AssetName(%s,%s) = %q,%v", c.os, c.arch, got, ok)
		}
	}
}

func TestAllowedURL(t *testing.T) {
	cases := []struct {
		raw      string
		redirect bool
		want     bool
	}{
		{"https://github.com/medeirosvictor/hermec/releases/download/v1.0.0/x.zip", false, true},
		{"https://github.com/medeirosvictor/hermec/releases/download/v1.0.0/x.zip", true, true},
		{"https://objects.githubusercontent.com/github-production-release-asset/1", true, true},
		{"https://release-assets.githubusercontent.com/github-production-release-asset/1", true, true},
		{"https://objects.githubusercontent.com/x", false, false}, // the CDN is only a redirect target
		{"http://github.com/medeirosvictor/hermec/releases/download/v1/x", false, false},
		{"https://github.com/other/hermec/releases/download/v1/x", false, false},
		{"https://github.com/medeirosvictor/hermec/releases/download/../../../x", false, false},
		{"https://github.com:8443/medeirosvictor/hermec/releases/download/v1/x", false, false},
		{"https://user@github.com/medeirosvictor/hermec/releases/download/v1/x", false, false},
		{"https://github.com.evil.com/medeirosvictor/hermec/releases/download/v1/x", false, false},
		{"https://evilobjects.githubusercontent.com/x", true, false},
		{"https://objects.githubusercontent.com.evil.com/x", true, false},
		{"https://127.0.0.1/x", true, false},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowedURL(u, c.redirect); got != c.want {
			t.Errorf("allowedURL(%s, redirect=%v) = %v", c.raw, c.redirect, got)
		}
	}
}

// ---- archives ----

func zipOf(t *testing.T, files map[string]string, order ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range order {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	zw.Close()
	return b.Bytes()
}

func tarOf(t *testing.T, hdrs []tar.Header, bodies []string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for i, h := range hdrs {
		h := h
		h.Size = int64(len(bodies[i]))
		if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeDir {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(bodies[i]))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func zipExtract(b []byte, bin string, w io.Writer) error {
	return extractZip(bytes.NewReader(b), int64(len(b)), bin, w)
}

func tarExtract(b []byte, bin string, w io.Writer) error {
	return extractTarGz(bytes.NewReader(b), bin, w)
}

func TestExtractZip(t *testing.T) {
	ok := zipOf(t, map[string]string{"hermec.exe": "BIN", "README.txt": "r"}, "hermec.exe", "README.txt")
	var out bytes.Buffer
	if err := zipExtract(ok, "hermec.exe", &out); err != nil || out.String() != "BIN" {
		t.Fatalf("ok: %q %v", out.String(), err)
	}
	bad := map[string][]byte{
		"missing":   zipOf(t, map[string]string{"README.txt": "r"}, "README.txt"),
		"traversal": zipOf(t, map[string]string{"hermec.exe": "B", "../evil": "x"}, "hermec.exe", "../evil"),
		"absolute":  zipOf(t, map[string]string{"hermec.exe": "B", "/evil": "x"}, "hermec.exe", "/evil"),
		"backslash": zipOf(t, map[string]string{"hermec.exe": "B", `a\..\evil`: "x"}, "hermec.exe", `a\..\evil`),
		"nested":    zipOf(t, map[string]string{"dir/hermec.exe": "B"}, "dir/hermec.exe"),
		"empty":     zipOf(t, map[string]string{"hermec.exe": ""}, "hermec.exe"),
		"duplicate": zipOf(t, map[string]string{"hermec.exe": "B"}, "hermec.exe", "hermec.exe"),
		"notzip":    []byte("hello"),
	}
	for name, b := range bad {
		if err := zipExtract(b, "hermec.exe", &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	old := maxBinary
	maxBinary = 2
	defer func() { maxBinary = old }()
	if err := zipExtract(ok, "hermec.exe", &bytes.Buffer{}); err == nil {
		t.Error("oversized entry accepted")
	}
}
