package tarutils

import (
	"archive/tar"
	"crypto/sha256"
	"io"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Tar interface {
	// Add directory entry to a tar archive. Handles symbolic links and
	// device files correctly. The prefix will be stripped from path.
	CopyTarEntry(w *tar.Writer, path string) (err error)

	// Create a tar archive from a given path. The prefix will be stripped
	// from path.
	CreateTarHash(tarball string, path string, prefix string) (checksum []byte, err error)

	// Create a tar archive from a given path and return its sha256
	// checksum. The prefix will be stripped from path.
	CreateTar(tarball string, path string, prefix string) (err error)

	// Write the tar header for a directory entry to a tar archive. Handles
	// symbolic links and device files correctly.
	WriteTarHeader(w *tar.Writer, path string, headerName string, f os.FileInfo) error

	// Extract tar archive.
	ExtractTar(tarball string, path string) error

	// Extract a directory from a tar archive.
	ExtractDir(path string, header *tar.Header) (err error)

	// Extract regular file from a tar archive.
	ExtractReg(path string, header *tar.Header, r *tar.Reader) (err error)

	// Extract symlink file from a tar archive.
	ExtractSymlink(path string, header *tar.Header) (err error)

	// Test whether a tar archive is empty.
	IsEmptyTar(tar string) (bool, error)

	// Takes care to return a correct entry for the Name field in a tar
	// header struct.
	TarHeaderEntry(f os.FileInfo, path string, prefix string) (entry string)
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

func CreateTarHash(tarball string, path string, prefix string) (checksum []byte, err error) {
	f, err := os.Create(tarball)
	if err != nil {
		return
	}
	defer f.Close()

	h := sha256.New()
	mw := io.MultiWriter(h, f)
	w := tar.NewWriter(mw)

	err = TarDir(w, path, prefix)
	if err != nil {
		return
	}

	if err = w.Close(); err != nil {
		return
	}

	checksum = h.Sum(nil)
	return
}

func CreateTar(tarball string, path string, prefix string) (err error) {
	f, err := os.Create(tarball)
	if err != nil {
		return
	}
	defer f.Close()

	w := tar.NewWriter(f)

	err = TarDir(w, path, prefix)
	if err = w.Close(); err != nil {
		return
	}

	return
}

func WriteTarHeader(w *tar.Writer, path string, headerName string, f os.FileInfo) (err error) {
	var link string

	if f.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(path)
		if err != nil {
			return
		}
	}

	header, err := tar.FileInfoHeader(f, link)
	if err != nil {
		return
	}

	header.Name = headerName

	return w.WriteHeader(header)
}

func CopyTarEntry(w *tar.Writer, path string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return
	}

	if _, err = io.Copy(w, f); err != nil {
		return
	}

	if err = f.Close(); err != nil {
		return
	}

	return
}

func TarDir(w *tar.Writer, path string, prefix string) error {
	return filepath.Walk(path, func(entry string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		s := TarHeaderEntry(f, entry, prefix)
		if s == "" {
			return nil
		}

		mode := f.Mode()
		if err := WriteTarHeader(w, entry, s, f); err != nil {
			return err
		}

		if (mode&os.ModeSymlink == os.ModeSymlink) || (mode&os.ModeDevice == os.ModeDevice) || f.IsDir() {
			return nil
		}

		if err := CopyTarEntry(w, entry); err != nil {
			return err
		}

		return nil
	})
}

func TarHeaderEntry(f os.FileInfo, path string, prefix string) (entry string) {
	entry = strings.TrimPrefix(path, prefix)
	if entry == "" || entry == "/" {
		return
	}

	if entry[0:1] == "/" {
		entry = entry[1:]
	}

	if f.IsDir() && (entry[len(entry)-1:len(entry)] != "/") {
		entry = entry + "/"
	}

	return
}

func ExtractTar(tarball string, path string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()

	r := tar.NewReader(f)
	if err != nil {
		return err
	}

	for header, err := r.Next(); err != io.EOF; header, err = r.Next() {
		if err != nil {
			break
		}

		if header.Typeflag&tar.TypeDir == tar.TypeDir {
			if err := ExtractDir(path, header); err != nil {
				return err
			}
		} else if header.Typeflag&tar.TypeSymlink == tar.TypeSymlink {
			if err := ExtractSymlink(path, header); err != nil {
				return err
			}
		} else {
			if err := ExtractReg(path, header, r); err != nil {
				return err
			}
		}
	}

	return err
}

func ExtractDir(path string, header *tar.Header) (err error) {
	entry := filepath.Join(path, header.Name)
	fi := header.FileInfo()

	err = os.MkdirAll(entry, fi.Mode())
	if err != nil {
		return
	}

	if err = os.Chown(entry, header.Uid, header.Gid); err != nil {
		return
	}

	for attr, data := range header.Xattrs {
		if err = syscall.Setxattr(entry, attr, []byte(data), 0); err != nil {
			return
		}
	}

	if err = os.Chtimes(entry, time.Now(), fi.ModTime()); err != nil {
		return
	}

	return
}

func ExtractReg(path string, header *tar.Header, r *tar.Reader) (err error) {
	fi := header.FileInfo()
	entry := filepath.Join(path, header.Name)
	filedir := filepath.Join(path, filepath.Dir(header.Name))

	err = os.MkdirAll(filedir, fi.Mode())
	if err != nil {
		return
	}

	g, err := os.OpenFile(entry, os.O_EXCL|os.O_WRONLY|os.O_CREATE, fi.Mode())
	if err != nil {
		return
	}

	w, err := io.Copy(g, r)
	if err != nil {
		return
	}
	if w != fi.Size() {
		return fmt.Errorf("Expected to write %d bytes, only wrote %d\n", fi.Size(), w)
	}

	if w != header.Size {
		return
	}

	if err = g.Close(); err != nil {
		return
	}

	if err := os.Chown(entry, header.Uid, header.Gid); err != nil {
		return err
	}

	for attr, data := range header.Xattrs {
		if err = syscall.Setxattr(entry, attr, []byte(data), 0); err != nil {
			return err
		}
	}

	if err = os.Chtimes(entry, fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	return
}

func ExtractSymlink(path string, header *tar.Header) (err error) {
	fi := header.FileInfo()
	entry := filepath.Join(path, header.Name)
	filedir := filepath.Join(path, filepath.Dir(header.Name))

	err = os.MkdirAll(filedir, fi.Mode())
	if err != nil {
		return
	}

	if err = os.Symlink(header.Linkname, entry); err != nil {
		return
	}
	if err = os.Lchown(entry, header.Uid, header.Gid); err != nil {
		return
	}
	return
}
