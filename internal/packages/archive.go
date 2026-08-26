package packages

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractArchive(src, dst, manifestName string) error {
	format, err := sniffArchive(src)
	if err != nil {
		return err
	}
	switch format {
	case "tar", "tar.gz":
		return extractTarArchive(src, dst, manifestName, format == "tar.gz")
	case "zip":
		return extractZipArchive(src, dst, manifestName)
	default:
		return fmt.Errorf("packages: unsupported archive format: %s", src)
	}
}

func sniffArchive(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("packages: open archive: %w", err)
	}
	defer f.Close()
	head := make([]byte, 262)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("packages: read archive: %w", err)
	}
	head = head[:n]
	switch {
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return "tar.gz", nil
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{'P', 'K', 3, 4}):
		return "zip", nil
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{'P', 'K', 5, 6}):
		return "zip", nil
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return "tar", nil
	default:
		return "", fmt.Errorf("packages: unrecognized archive: %s", path)
	}
}

func extractTarArchive(src, dst, manifestName string, gzipped bool) error {
	names, err := tarEntryNames(src, gzipped)
	if err != nil {
		return err
	}
	strip := topDirToStrip(names, manifestName)
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("packages: open archive: %w", err)
	}
	defer f.Close()
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("packages: read archive: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("packages: read archive: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeDir:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("packages: archive: link entry %q rejected", hdr.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("packages: archive: special entry %q rejected", hdr.Name)
		case tar.TypeReg, tar.TypeRegA:
		default:
			return fmt.Errorf("packages: archive: unsupported entry %q", hdr.Name)
		}
		name, err := archiveEntryName(hdr.Name)
		if err != nil {
			return err
		}
		name, ok := stripEntry(name, strip)
		if !ok {
			continue
		}
		if err := writeArchiveFile(dst, name, tr); err != nil {
			return err
		}
	}
	return nil
}

func tarEntryNames(src string, gzipped bool) ([]string, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("packages: open archive: %w", err)
	}
	defer f.Close()
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("packages: read archive: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	names := []string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("packages: read archive: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeDir:
			continue
		}
		name, err := archiveEntryName(hdr.Name)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func extractZipArchive(src, dst, manifestName string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("packages: open zip: %w", err)
	}
	defer zr.Close()
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, err := archiveEntryName(f.Name)
		if err != nil {
			return err
		}
		names = append(names, name)
	}
	strip := topDirToStrip(names, manifestName)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, err := archiveEntryName(f.Name)
		if err != nil {
			return err
		}
		name, ok := stripEntry(name, strip)
		if !ok {
			continue
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("packages: archive: symlink entry %q rejected", f.Name)
		}
		if mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 {
			return fmt.Errorf("packages: archive: special entry %q rejected", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("packages: archive: open %s: %w", f.Name, err)
		}
		err = writeArchiveFile(dst, name, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeArchiveFile(dst, name string, r io.Reader) error {
	target, err := joinArchivePath(dst, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("packages: archive: mkdir: %w", err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("packages: archive: write %s: %w", name, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return fmt.Errorf("packages: archive: write %s: %w", name, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("packages: archive: write %s: %w", name, err)
	}
	return nil
}

func archiveEntryName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if name == "" {
		return "", fmt.Errorf("packages: archive: empty entry name")
	}
	if !cleanRelativePath(name) {
		return "", fmt.Errorf("packages: archive: unsafe entry name %q", name)
	}
	return name, nil
}

func joinArchivePath(dst, name string) (string, error) {
	target := filepath.Join(dst, filepath.FromSlash(name))
	rel, err := filepath.Rel(dst, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("packages: archive: entry escapes destination: %q", name)
	}
	return target, nil
}

func topDirToStrip(names []string, manifestName string) string {
	for _, n := range names {
		if n == manifestName {
			return ""
		}
	}
	var top string
	for _, n := range names {
		if !strings.Contains(n, "/") {
			continue
		}
		first := strings.SplitN(n, "/", 2)[0]
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	if top == "" {
		return ""
	}
	for _, n := range names {
		if n == top+"/"+manifestName {
			return top
		}
	}
	return ""
}

func stripEntry(name, strip string) (string, bool) {
	if strip == "" {
		return name, true
	}
	if !strings.HasPrefix(name, strip+"/") {
		return "", false
	}
	name = strings.TrimPrefix(name, strip+"/")
	if name == "" {
		return "", false
	}
	return name, true
}
