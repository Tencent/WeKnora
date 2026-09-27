//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// LandlockABI is the version of Landlock the kernel offers, 0 when it has
// none (before Linux 5.13, or not enabled) or a seccomp filter refuses it.
func LandlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// Rights to files, by the Landlock ABI that knows them.
const (
	fsRightsV1 = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM

	// readRights: read and run.
	readRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR
	// writeRights: read, write and create; not run, nor make devices.
	writeRights = unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE |
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	// fileRights are those a rule on a file, not a directory, may grant.
	fileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE |
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// landlockRuleNetPort and landlockNetPortAttr are the kernel's, which x/sys
// does not have yet.
const landlockRuleNetPort = 2

type landlockNetPortAttr struct {
	allowedAccess uint64
	port          uint64
}

// fsRights are the rights to files a ruleset handles under an ABI: what it
// does not grant is denied.
func fsRights(abi int) uint64 {
	r := uint64(fsRightsV1)
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return r
}

// restrict confines this thread, and what it execs, to what s allows: its
// Read and Write paths, TCP connections to its ConnectPorts when it limits
// TCP (and the kernel can), and from Linux 6.12 no signals or abstract unix
// sockets outside the sandbox.
func restrict(s Spec) error {
	abi := LandlockABI()
	if abi < 1 {
		return errors.New("the kernel offers no Landlock")
	}
	handled := fsRights(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	if s.LimitTCP && abi >= 4 {
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	}
	if abi >= 6 {
		attr.Scoped = unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create a ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer func() { _ = unix.Close(ruleset) }()
	for _, p := range s.Read {
		if err := allowPath(ruleset, p, readRights&handled); err != nil {
			return err
		}
	}
	for _, p := range s.Write {
		if err := allowPath(ruleset, p, writeRights&handled); err != nil {
			return err
		}
	}
	if attr.Access_net != 0 {
		for _, port := range s.ConnectPorts {
			rule := landlockNetPortAttr{allowedAccess: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, port: uint64(port)}
			if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset),
				landlockRuleNetPort, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
				return fmt.Errorf("allow TCP port %d: %w", port, errno)
			}
		}
	}
	// Required to restrict oneself without CAP_SYS_ADMIN; it also keeps
	// setuid programs from gaining rights in the sandbox.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("restrict: %w", errno)
	}
	return nil
}

// allowPath grants rights beneath path, or to the file it is; a path that
// does not exist is left out.
func allowPath(ruleset int, path string, rights uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		rights &= fileRights
	}
	rule := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset),
		unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("allow %s: %w", path, errno)
	}
	return nil
}
