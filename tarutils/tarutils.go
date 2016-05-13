package tarutils

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Tar interface {
	AddEntry(w *tar.Writer, path string, strip string) error
	WriteTarHeader(tw *tar.Writer, statPath string, entry string, fi os.FileInfo) error
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

func AddEntry(w *tar.Writer, path string, strip string) error {
	entry := strings.TrimPrefix(path, strip)
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

func TarDir(w *tar.Writer, path string, strip string) error {
	return filepath.Walk(path, func(entry string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if err := AddEntry(w, entry, strip); err != nil {
			return err
		}

		return nil
	})
}
