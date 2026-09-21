// Package core holds the platform-neutral sandbox contract: policy, path
// checks, workspace identity, and denial classification. Platform backends
// live in sibling packages.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// NetworkMode is the coarse network stance. The sandbox layer decides whether
// traffic can leave at all; deciding which hosts are reachable is a proxy's
// job and is out of scope here.
type NetworkMode int

const (
	// NetworkDenied blocks all traffic. Default.
	NetworkDenied NetworkMode = iota
	// NetworkLoopback permits only the ports listed in Policy.LoopbackPorts.
	NetworkLoopback
	// NetworkUnrestricted applies no network restriction.
	NetworkUnrestricted
)

// WritableRoot is one writable subtree plus the read-only carve-outs inside it.
type WritableRoot struct {
	// Path is an absolute, cleaned host path.
	Path string
	// ReadOnlySubpaths sit inside Path but must not be written, typically
	// Path/.git.
	ReadOnlySubpaths []string
}

// Policy is the platform-neutral permission declaration. It is a value
// object: comparable by Fingerprint, free of side effects, safe to copy.
type Policy struct {
	WritableRoots []WritableRoot
	// ReadableRoots are re-opened after PrivateRoots is denied, so this is
	// also the list that decides what survives inside a private subtree.
	// An entry may name a file (a shell startup script), not just a directory.
	ReadableRoots []string
	// PrivateRoots are subtrees denied read wholesale — in practice the user's
	// home directory. WritableRoots and ReadableRoots are re-opened inside
	// them; DenyRead still wins over both.
	//
	// This exists because macOS cannot enforce a read allowlist for us: the
	// base profile must grant blanket file-read* or sandbox-exec aborts the
	// child (see seatbelt/base.sbpl). Denying the one subtree that holds the
	// user's data is what turns that back into a meaningful boundary.
	PrivateRoots  []string
	DenyRead      []string
	Network       NetworkMode
	LoopbackPorts []int
	Cwd           string
}

var (
	// ErrNoWritableRoot is returned when a policy names no writable root.
	ErrNoWritableRoot = errors.New("localsandbox: policy has no writable root")
	// ErrFilesystemRoot is returned when a policy target is "/" or a volume root.
	ErrFilesystemRoot = errors.New("localsandbox: a filesystem root cannot be a policy target")
	// ErrCwdOutsideRoots is returned when Policy.Cwd is outside every writable root.
	ErrCwdOutsideRoots = errors.New("localsandbox: cwd is outside every writable root")
	// ErrRelativePath is returned when a policy path is not absolute.
	ErrRelativePath = errors.New("localsandbox: policy paths must be absolute")
	// ErrDenyReadCoversRoot is returned when a deny-read entry covers a writable root.
	ErrDenyReadCoversRoot = errors.New("localsandbox: deny-read entry covers a writable root")
	// ErrWorkspaceTooBroad is returned when a workspace or grant is $HOME,
	// an ancestor of $HOME, or another well-known wide root. Sandboxing
	// those paths would re-open the private home (or every user) as the
	// workspace and collapse the read boundary to a credential denylist.
	ErrWorkspaceTooBroad = errors.New("localsandbox: workspace is too broad to sandbox")
)

// wellKnownWideRoots cannot be a workspace, a Relax write grant, or a
// writable root. Exact match only: /tmp/project is fine, /tmp is not.
// /private/var and /System/Volumes/Data are listed separately because they
// are not the same path as /private or /System.
var wellKnownWideRoots = []string{
	"/Users", "/Volumes", "/private", "/tmp", "/var", "/etc",
	"/System", "/Library", "/opt", "/home",
	"/private/var", "/private/tmp", "/private/etc",
	"/var/folders",
	"/System/Volumes", "/System/Volumes/Data",
}

func isWellKnownWideRoot(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	for _, wide := range wellKnownWideRoots {
		if !filepath.IsAbs(wide) {
			continue
		}
		if PathUnder(p, filepath.Clean(wide)) && PathUnder(filepath.Clean(wide), p) {
			return true
		}
	}
	return false
}

// caseInsensitivePaths reports whether path comparison must ignore case.
// APFS is case-insensitive by default, so this is not a Windows-only concern.
func caseInsensitivePaths() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// isFilesystemRoot reports whether p is "/" or a volume root such as "C:\".
func isFilesystemRoot(p string) bool {
	return filepath.Dir(p) == p
}

// PathUnder reports whether child is root itself or lies beneath it. Both
// arguments must already be absolute and cleaned.
func PathUnder(child, root string) bool {
	if child == "" || root == "" {
		return false
	}
	if caseInsensitivePaths() {
		child = strings.ToLower(child)
		root = strings.ToLower(root)
	}
	if child == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(child, root)
}

func checkAbsolute(paths ...string) error {
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("%w: %q", ErrRelativePath, p)
		}
	}
	return nil
}

// Validate enforces the invariants every backend relies on.
// Ask mode may have no writable roots; cwd must then sit under a readable root.
func (p Policy) Validate() error {
	for _, root := range p.WritableRoots {
		if err := checkAbsolute(root.Path); err != nil {
			return err
		}
		if isFilesystemRoot(root.Path) {
			return fmt.Errorf("%w: writable root %q", ErrFilesystemRoot, root.Path)
		}
		if isWellKnownWideRoot(root.Path) {
			return fmt.Errorf("%w: writable root %q", ErrWorkspaceTooBroad, root.Path)
		}
		if err := checkAbsolute(root.ReadOnlySubpaths...); err != nil {
			return err
		}
		for _, sub := range root.ReadOnlySubpaths {
			if !PathUnder(sub, root.Path) {
				return fmt.Errorf(
					"localsandbox: read-only subpath %q is outside writable root %q", sub, root.Path)
			}
		}
	}
	if err := checkAbsolute(p.ReadableRoots...); err != nil {
		return err
	}
	if err := checkAbsolute(p.PrivateRoots...); err != nil {
		return err
	}
	// A private root covering a writable root is the normal case: the project
	// lives in the home directory. Only "/" is refused, because denying it
	// would also cut off the system paths exec itself reads.
	for _, private := range p.PrivateRoots {
		if isFilesystemRoot(private) {
			return fmt.Errorf("%w: private root %q", ErrFilesystemRoot, private)
		}
	}
	if err := checkAbsolute(p.DenyRead...); err != nil {
		return err
	}
	for _, deny := range p.DenyRead {
		if isFilesystemRoot(deny) {
			return fmt.Errorf("%w: deny-read %q", ErrFilesystemRoot, deny)
		}
		for _, root := range p.WritableRoots {
			if PathUnder(root.Path, deny) {
				return fmt.Errorf("%w: %q covers %q", ErrDenyReadCoversRoot, deny, root.Path)
			}
		}
	}
	if err := checkAbsolute(p.Cwd); err != nil {
		return err
	}
	if len(p.WritableRoots) == 0 {
		for _, root := range p.ReadableRoots {
			if PathUnder(p.Cwd, root) {
				return nil
			}
		}
		return ErrNoWritableRoot
	}
	for _, root := range p.WritableRoots {
		if PathUnder(p.Cwd, root.Path) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrCwdOutsideRoots, p.Cwd)
}

// Fingerprint is a stable digest used to decide whether an existing Prepared
// still matches the requested policy. Ordering is normalised so that two
// policies describing the same permissions hash identically.
func (p Policy) Fingerprint() string {
	norm := p.canonical()
	encoded, err := json.Marshal(norm)
	if err != nil {
		// json.Marshal on this shape cannot fail; keep the signature simple.
		panic(fmt.Sprintf("localsandbox: fingerprint encode: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (p Policy) canonical() Policy {
	out := Policy{
		WritableRoots: make([]WritableRoot, len(p.WritableRoots)),
		ReadableRoots: append([]string(nil), p.ReadableRoots...),
		PrivateRoots:  append([]string(nil), p.PrivateRoots...),
		DenyRead:      append([]string(nil), p.DenyRead...),
		Network:       p.Network,
		LoopbackPorts: append([]int(nil), p.LoopbackPorts...),
		Cwd:           p.Cwd,
	}
	for i, root := range p.WritableRoots {
		subs := append([]string(nil), root.ReadOnlySubpaths...)
		sort.Strings(subs)
		out.WritableRoots[i] = WritableRoot{Path: root.Path, ReadOnlySubpaths: subs}
	}
	sort.Slice(out.WritableRoots, func(i, j int) bool {
		return out.WritableRoots[i].Path < out.WritableRoots[j].Path
	})
	sort.Strings(out.ReadableRoots)
	sort.Strings(out.PrivateRoots)
	sort.Strings(out.DenyRead)
	sort.Ints(out.LoopbackPorts)
	return out
}
