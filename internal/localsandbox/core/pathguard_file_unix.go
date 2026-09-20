//go:build unix

package core

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func (g *PathGuard) writeRel(root, rel, orig string, data []byte, perm os.FileMode) error {
	fd, err := walkOpen(root, rel, true, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC, uint32(perm.Perm()))
	if err != nil {
		return mapWalkErr(orig, err)
	}
	f := os.NewFile(uintptr(fd), orig)
	defer f.Close()
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n < len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (g *PathGuard) readRel(root, rel, orig string) ([]byte, error) {
	fd, err := walkOpen(root, rel, false, unix.O_RDONLY, 0)
	if err != nil {
		return nil, mapWalkErr(orig, err)
	}
	f := os.NewFile(uintptr(fd), orig)
	defer f.Close()
	return io.ReadAll(f)
}

func (g *PathGuard) mkdirRel(root, rel, orig string, perm os.FileMode) error {
	if rel == "." {
		return nil
	}
	fd, err := walkOpen(root, rel, true, unix.O_RDONLY|unix.O_DIRECTORY, uint32(perm.Perm()))
	if err != nil {
		return mapWalkErr(orig, err)
	}
	return unix.Close(fd)
}

func (g *PathGuard) lstatRel(root, rel, orig string) (os.FileInfo, error) {
	fd, err := walkOpen(root, rel, false, unix.O_RDONLY, 0)
	if err != nil {
		return nil, mapWalkErr(orig, err)
	}
	f := os.NewFile(uintptr(fd), orig)
	defer f.Close()
	return f.Stat()
}

func walkOpen(root, rel string, mkdir bool, lastFlags int, lastPerm uint32) (int, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, mapRootOpenErr(root, err)
	}
	parts := relParts(rel)
	if len(parts) == 0 {
		return fd, nil
	}
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			_ = unix.Close(fd)
			return -1, ErrPathDenied
		}
		last := i == len(parts)-1
		if mkdir && (!last || lastFlags&unix.O_DIRECTORY != 0) {
			mode := uint32(0o755)
			if last && lastPerm != 0 {
				mode = lastPerm
			}
			if err := unix.Mkdirat(fd, part, mode); err != nil && !os.IsExist(err) {
				_ = unix.Close(fd)
				return -1, err
			}
		}
		flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		perm := uint32(0)
		if last {
			flags = lastFlags | unix.O_NOFOLLOW | unix.O_CLOEXEC
			perm = lastPerm
		}
		next, err := unix.Openat(fd, part, flags, perm)
		if err != nil {
			mapped := mapOpenatErr(fd, part, err)
			_ = unix.Close(fd)
			return -1, mapped
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, nil
}

func mapRootOpenErr(root string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ELOOP) {
		return ErrPathDenied
	}
	var st unix.Stat_t
	if e := unix.Lstat(root, &st); e == nil {
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return ErrPathDenied
		}
	}
	return err
}

func mapOpenatErr(dirfd int, name string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ELOOP) {
		return ErrPathDenied
	}
	var st unix.Stat_t
	if e := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); e == nil {
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return ErrPathDenied
		}
	}
	return err
}

func mapWalkErr(path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPathDenied) {
		return fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%w: %q", ErrPathDenied, path)
	}
	return err
}
