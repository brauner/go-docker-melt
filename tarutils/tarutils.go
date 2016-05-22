package tarutils

import (
	"archive/tar"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Tar interface {
	// Add directory entry to a tar archive. Handles symbolic links and
	// device files correctly. The prefix will be stripped from path.
	AddEntry(w *tar.Writer, path string, prefix string) error

	// Create a tar archive from a given path. The prefix will be stripped
	// from path.
	CreateTar(path string, prefix string) error

	// Create a tar archive from a given path and return its sha256
	// checksum. The prefix will be stripped from path.
	CreateTarHash(path string, prefix string) error

	// Write the tar header for a directory entry to a tar archive. Handles
	// symbolic links and device files correctly.
	WriteTarHeader(tw *tar.Writer, statPath string, entry string, fi os.FileInfo) error

	// TODO: Add functions to extract tar archives.

	// Test whether a tar archive is empty.
	IsEmptyTar(tar string) (bool, error)
}

func IsEmptyTar(tarball string) (bool, error) {
	f, err := os.Open(tarball)
	if err != nil {
		return false, err
	}
	defer f.Close()
	t := tar.NewReader(f)
	_, err = t.Next()
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func CreateTarHash(path string, prefix string) ([]byte, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	mw := io.MultiWriter(h, f)
	w := tar.NewWriter(mw)
	defer w.Close()

	err = TarDir(w, path, prefix)
	if err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func CreateTar(path string, prefix string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := tar.NewWriter(f)
	defer w.Close()
	return TarDir(w, path, prefix)
}

func WriteTarHeader(tw *tar.Writer, statPath string, entry string, fi os.FileInfo) error {
	var (
		link string
		err  error
	)
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(statPath)
		if err != nil {
			return err
		}
	}
	header, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	header.Name = entry
	if err = tw.WriteHeader(header); err != nil {
		return err
	}
	return nil
}

func AddEntry(w *tar.Writer, path string, prefix string) error {
	entry := strings.TrimPrefix(path, prefix)
	if entry == "" || entry == "/" {
		return nil
	}
	if entry[0:1] == "/" {
		entry = entry[1:]
	}

	stat, err := os.Lstat(path)
	if err != nil {
		return err
	}

	mode := stat.Mode()
	if (mode&os.ModeSymlink == os.ModeSymlink) || (mode&os.ModeDevice == os.ModeDevice) {
		if err := WriteTarHeader(w, path, entry, stat); err != nil {
			return err
		}
		return nil
	}

	if err := WriteTarHeader(w, path, entry, stat); err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if stat.IsDir() {
		return nil
	}

	if _, err := io.Copy(w, f); err != nil {
		return err
	}

	return nil
}

func TarDir(w *tar.Writer, path string, prefix string) error {
	return filepath.Walk(path, func(entry string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if err := AddEntry(w, entry, prefix); err != nil {
			return err
		}

		return nil
	})
}
