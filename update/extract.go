package update

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// safeName reports whether an archive entry name stays inside the archive:
// relative, forward slashes, no ".." element, no drive or stream colon.
func safeName(name string) bool {
	if name == "" || strings.ContainsAny(name, `\:`+"\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, el := range strings.Split(name, "/") {
		if el == ".." {
			return false
		}
	}
	return true
}

// copyBinary copies at most maxBinary bytes of r to w, and fails on an empty
// or oversized file.
func copyBinary(w io.Writer, r io.Reader) error {
	n, err := io.Copy(w, io.LimitReader(r, maxBinary+1))
	if err != nil {
		return err
	}
	if n == 0 || n > maxBinary {
		return errors.New("update: binary has a bad size")
	}
	return nil
}

// extractZip writes the entry named bin (at the archive root) of the zip at
// file to w. Any unsafe or non-regular entry anywhere rejects the archive.
func extractZip(file, bin string, w io.Writer) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	var found *zip.File
	for _, f := range zr.File {
		if !safeName(f.Name) {
			return fmt.Errorf("update: unsafe archive entry %q", f.Name)
		}
		if f.Mode()&fs.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.Mode().IsDir()) {
			return fmt.Errorf("update: archive entry %q is not a plain file", f.Name)
		}
		if path.Clean(f.Name) != bin || f.Mode().IsDir() {
			continue
		}
		if found != nil {
			return fmt.Errorf("update: archive lists %s twice", bin)
		}
		if f.UncompressedSize64 > uint64(maxBinary) {
			return errors.New("update: binary has a bad size")
		}
		found = f
	}
	if found == nil {
		return fmt.Errorf("update: archive has no %s", bin)
	}
	rc, err := found.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return copyBinary(w, rc)
}
