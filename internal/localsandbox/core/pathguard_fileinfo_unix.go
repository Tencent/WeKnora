//go:build unix

package core

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type unixFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	sys     unix.Stat_t
}

func (fi unixFileInfo) Name() string       { return fi.name }
func (fi unixFileInfo) Size() int64        { return fi.size }
func (fi unixFileInfo) Mode() os.FileMode  { return fi.mode }
func (fi unixFileInfo) ModTime() time.Time { return fi.modTime }
func (fi unixFileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi unixFileInfo) Sys() any           { return &fi.sys }

func fileInfoFromUnixStat(name string, st unix.Stat_t) os.FileInfo {
	mode := os.FileMode(st.Mode & 0o777)
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	case unix.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	}
	if st.Mode&unix.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if st.Mode&unix.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if st.Mode&unix.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return unixFileInfo{
		name:    name,
		size:    st.Size,
		mode:    mode,
		modTime: statModTime(st),
		sys:     st,
	}
}
