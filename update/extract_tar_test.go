package update

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestExtractTarGz(t *testing.T) {
	reg := func(n string) tar.Header { return tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o755} }
	ok := tarOf(t, []tar.Header{reg("hermec"), reg("README.txt")}, []string{"BIN", "r"})
	var out bytes.Buffer
	if err := extractTarGz(writeTemp(t, ok), "hermec", &out); err != nil || out.String() != "BIN" {
		t.Fatalf("ok: %q %v", out.String(), err)
	}
	dotSlash := tarOf(t, []tar.Header{reg("./hermec")}, []string{"BIN"})
	out.Reset()
	if err := extractTarGz(writeTemp(t, dotSlash), "hermec", &out); err != nil || out.String() != "BIN" {
		t.Fatalf("./hermec: %q %v", out.String(), err)
	}
	sym := tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}
	hard := tar.Header{Name: "hl", Typeflag: tar.TypeLink, Linkname: "hermec"}
	bad := map[string][]byte{
		"missing":   tarOf(t, []tar.Header{reg("README.txt")}, []string{"r"}),
		"traversal": tarOf(t, []tar.Header{reg("hermec"), reg("../evil")}, []string{"B", "x"}),
		"absolute":  tarOf(t, []tar.Header{reg("hermec"), reg("/evil")}, []string{"B", "x"}),
		"symlink":   tarOf(t, []tar.Header{reg("hermec"), sym}, []string{"B", ""}),
		"hardlink":  tarOf(t, []tar.Header{reg("hermec"), hard}, []string{"B", ""}),
		"symlinked": tarOf(t, []tar.Header{{Name: "hermec", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}}, []string{""}),
		"nested":    tarOf(t, []tar.Header{reg("dir/hermec")}, []string{"B"}),
		"duplicate": tarOf(t, []tar.Header{reg("hermec"), reg("hermec")}, []string{"B", "B"}),
		"notgz":     []byte("hello"),
	}
	for name, b := range bad {
		if err := extractTarGz(writeTemp(t, b), "hermec", &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	old := maxBinary
	maxBinary = 2
	defer func() { maxBinary = old }()
	if err := extractTarGz(writeTemp(t, ok), "hermec", &bytes.Buffer{}); err == nil {
		t.Error("oversized entry accepted")
	}
}
