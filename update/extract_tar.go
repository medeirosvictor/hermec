package update

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
)

// extractTarGz is extractZip for a .tar.gz. The total inflated size is capped
// so a compression bomb cannot fill the disk through the other entries.
func extractTarGz(r io.Reader, bin string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, 2*maxBinary))
	done := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !safeName(h.Name) {
			return fmt.Errorf("update: unsafe archive entry %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("update: archive entry %q is not a plain file", h.Name)
		}
		if path.Clean(h.Name) != bin {
			continue
		}
		if done {
			return fmt.Errorf("update: archive lists %s twice", bin)
		}
		if h.Size > maxBinary {
			return errors.New("update: binary has a bad size")
		}
		if err := copyBinary(w, tr); err != nil {
			return err
		}
		done = true
	}
	if !done {
		return fmt.Errorf("update: archive has no %s", bin)
	}
	return nil
}
