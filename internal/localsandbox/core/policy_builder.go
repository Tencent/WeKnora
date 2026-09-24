package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApprovalMode is the user-facing permission stance for a turn.
type ApprovalMode string

const (
	// ModeAsk reads the workspace but asks before every write or command.
	ModeAsk ApprovalMode = "ask"
	// ModeAuto works freely inside the workspace and asks only when a command
	// needs to reach outside it. Default.
	ModeAuto ApprovalMode = "auto"
	// ModeFull runs without any sandbox.
	ModeFull ApprovalMode = "full"
)

// Known reports whether m names one of the three stances at all.
func (m ApprovalMode) Known() bool {
	switch m {
	case ModeAsk, ModeAuto, ModeFull:
		return true
	default:
		return false
	}
}

// Shipped reports whether this build can serve m end to end.
//
// This is the single source of truth for "which modes exist today", and it
// lives beside the enum rather than in whatever reads the preferences file.
// ModeAsk compiles to a valid policy with nothing writable and relies on an
// approval round-trip to widen it per command; ModeFull is the absence of a
// policy. Neither path is wired yet, so only ModeAuto is served.
func (m ApprovalMode) Shipped() bool {
	return m == ModeAuto
}

// ParseApprovalMode normalizes a stored preference.
//
// Unknown values fall back to ModeAuto so a typo cannot stop the agent.
// Known-but-unshipped modes are returned unchanged: mapping ask→auto would
// widen access, and Service refuses a mode it cannot honour.
func ParseApprovalMode(raw string) ApprovalMode {
	mode := ApprovalMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode.Known() {
		return mode
	}
	return ModeAuto
}

// ErrFullAccessHasNoPolicy signals that ModeFull is not a wide policy but the
// absence of one. Callers must branch on it explicitly.
var ErrFullAccessHasNoPolicy = errors.New("localsandbox: full access runs without a sandbox policy")

// ErrApprovalModeNotShipped reports a mode this build cannot enforce. It is a
// refusal, not a downgrade: ModeAsk is stricter than ModeAuto, so quietly
// running the turn under auto would widen access past what was asked for.
var ErrApprovalModeNotShipped = errors.New("localsandbox: approval mode is not available in this build")

// credentialDirNames are denied last, so they stay denied even when they sit
// inside a directory homeReadableNames re-opened (~/.cargo/credentials.toml is
// the motivating case).
var credentialDirNames = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".docker", ".npmrc", ".config/gh",
	".netrc", ".git-credentials", ".config/git/credentials", ".pypirc", ".password-store",
	".config/gcloud", ".azure", ".terraform.d",
	".cargo/credentials", ".cargo/credentials.toml", ".gem/credentials",
	".m2/settings.xml", ".gradle/gradle.properties",
	"Library/Keychains",
}

// homeReadableNames are re-opened after the home directory is denied.
//
// Without them the agent cannot run a toolchain the user installed per-user
// (nvm, pyenv, rustup...), which is most of them. The list is deliberately
// about build tooling: it is the smallest set that keeps ordinary development
// working, and anything not on it stays invisible.
var homeReadableNames = []string{
	".asdf", ".bun", ".cargo", ".deno", ".gem", ".gradle",
	".npm", ".nvm", ".pnpm-store", ".pyenv", ".rbenv", ".rustup",
	".sdkman", ".volta", ".yarn",
	// Only the bin dir: ~/.local/share holds app state and tokens.
	".local/bin",
}

// toolchainBinNames are prepended onto PATH so the agent can run per-user
// toolchains without sourcing shell rc files (those files often hold API keys).
var toolchainBinNames = []string{
	".cargo/bin", ".local/bin", ".deno/bin", ".bun/bin",
	".pyenv/shims", ".rbenv/shims", ".asdf/shims", ".yarn/bin",
}

// extraPrivateRoots tighten Seatbelt's blanket file-read* on darwin.
// /Users and /Volumes cover other homes and mounted disks. /var (and
// /private/var) covers /var/folders, where per-user temp tokens live.
// /tmp is not in this list: the base Seatbelt profile opens it for read
// and write. A private deny here is emitted after that allow and would
// close reads again.
var extraPrivateRoots = []string{
	"/Users", "/Volumes",
	"/var", "/private/var",
}

// platformReadRoots are extra readable paths for Windows / PathGuard.
// Darwin Seatbelt ignores them for availability: the base profile already
// grants unfiltered file-read*. Keeping the list makes the Policy fingerprint
// comparable across platforms.
var platformReadRoots = []string{
	"/usr/lib", "/usr/share", "/usr/bin", "/bin", "/sbin", "/usr/sbin",
	"/usr/libexec", "/System", "/Library/Apple", "/private/etc",
	"/opt/homebrew", "/usr/local",
}

// ErrInstallDirOutsideSkillsRoot refuses an install policy for any directory
// that is not inside the configured skills root.
var ErrInstallDirOutsideSkillsRoot = errors.New("localsandbox: install directory is outside the skills root")

// PolicyBuilder derives a Policy from an approval mode and a workspace.
type PolicyBuilder struct {
	homeDir    string
	appDataDir string
	skillsRoot string
}

// NewPolicyBuilder returns a builder scoped to the user's home and app-data dirs.
func NewPolicyBuilder(homeDir, appDataDir string) *PolicyBuilder {
	return &PolicyBuilder{
		homeDir:    filepath.Clean(homeDir),
		appDataDir: filepath.Clean(appDataDir),
	}
}

// Build derives a policy from the mode and the workspace. Pure: no IO, so it
// is directly unit-testable.
func (b *PolicyBuilder) Build(mode ApprovalMode, ws Workspace) (Policy, error) {
	if mode == ModeFull {
		return Policy{}, ErrFullAccessHasNoPolicy
	}
	if err := b.rejectBroadWorkspace(ws.Root); err != nil {
		return Policy{}, err
	}

	p := Policy{
		// Auto-mode chat commands need outbound access for package installs,
		// APIs, and skill tests. Ask mode keeps it denied below: a network
		// denial is what routes a command to the approval prompt.
		Network:       NetworkUnrestricted,
		Cwd:           ws.Root,
		ReadableRoots: b.readableRoots(ws),
		PrivateRoots:  b.privateRoots(),
		DenyRead:      b.denyRead(),
	}

	switch mode {
	case ModeAuto:
		root := WritableRoot{Path: ws.Root}
		if ws.ProtectGit {
			// Losing .git to a hallucinated rm -rf is unrecoverable, and it is
			// the least appropriate thing for an LLM to decide unilaterally.
			root.ReadOnlySubpaths = []string{filepath.Join(ws.Root, ".git")}
		}
		p.WritableRoots = []WritableRoot{root}
	case ModeAsk:
		// Nothing is writable and nothing leaves the machine up front;
		// approved commands are re-prepared through Relax. Cwd stays the
		// workspace so the process has a home.
		p.WritableRoots = nil
		p.Network = NetworkDenied
		p.Cwd = ws.Root
	default:
		return Policy{}, fmt.Errorf("localsandbox: unknown approval mode %q", mode)
	}

	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// privateRoots denies the user's home. Everything the agent legitimately
// needs inside it comes back through readableRoots.
func (b *PolicyBuilder) privateRoots() []string {
	var roots []string
	if b.homeDir != "" && b.homeDir != string(filepath.Separator) {
		roots = append(roots, b.homeDir)
	}
	for _, extra := range extraPrivateRoots {
		cleaned := filepath.Clean(extra)
		if !filepath.IsAbs(cleaned) || isFilesystemRoot(cleaned) {
			continue
		}
		if samePath(cleaned, b.homeDir) {
			continue
		}
		roots = append(roots, cleaned)
	}
	return roots
}

func (b *PolicyBuilder) readableRoots(ws Workspace) []string {
	roots := append([]string(nil), platformReadRoots...)
	roots = append(roots, ws.Root)
	for _, name := range homeReadableNames {
		roots = append(roots, filepath.Join(b.homeDir, filepath.FromSlash(name)))
	}
	if b.skillsRoot != "" {
		roots = append(roots, b.skillsRoot)
	}
	return roots
}

// WithSkillsRoot makes installed skills readable to every chat command.
func (b *PolicyBuilder) WithSkillsRoot(root string) *PolicyBuilder {
	if root = strings.TrimSpace(root); root != "" {
		b.skillsRoot = filepath.Clean(root)
	}
	return b
}

// BuildInstall is the policy one skill install runs under: only dir is
// writable, the network is open for package downloads, and deny-read is the
// same set auto mode enforces.
func (b *PolicyBuilder) BuildInstall(dir string) (Policy, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if b.skillsRoot == "" || !filepath.IsAbs(dir) ||
		!PathUnder(dir, b.skillsRoot) || samePath(dir, b.skillsRoot) {
		return Policy{}, fmt.Errorf("%w: %q", ErrInstallDirOutsideSkillsRoot, dir)
	}
	if err := b.rejectBroadWorkspace(dir); err != nil {
		return Policy{}, err
	}
	p := Policy{
		Network:       NetworkUnrestricted,
		Cwd:           dir,
		WritableRoots: []WritableRoot{{Path: dir}},
		ReadableRoots: b.readableRoots(Workspace{Root: dir}),
		PrivateRoots:  b.privateRoots(),
		DenyRead:      b.denyRead(),
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// ToolchainBins returns existing per-user and platform toolchain directories
// that should be prepended to PATH. It is the substitute for sourcing rc files.
func (b *PolicyBuilder) ToolchainBins() []string {
	var out []string
	for _, name := range toolchainBinNames {
		p := filepath.Join(b.homeDir, filepath.FromSlash(name))
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(b.homeDir, ".nvm", "versions", "node", "*", "bin")); err == nil {
		for _, p := range matches {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				out = append(out, p)
			}
		}
	}
	for _, p := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func (b *PolicyBuilder) rejectBroadWorkspace(root string) error {
	root = filepath.Clean(root)
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("%w: %q", ErrRelativePath, root)
	}
	if isFilesystemRoot(root) {
		return fmt.Errorf("%w: %q", ErrFilesystemRoot, root)
	}
	if b.homeDir != "" && b.homeDir != string(filepath.Separator) {
		home := filepath.Clean(b.homeDir)
		if PathUnder(home, root) {
			return fmt.Errorf("%w: %q covers the home directory", ErrWorkspaceTooBroad, root)
		}
		library := filepath.Join(home, "Library")
		if PathUnder(root, library) {
			return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, root)
		}
	}
	if isWellKnownWideRoot(root) {
		return fmt.Errorf("%w: %q", ErrWorkspaceTooBroad, root)
	}
	return nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return PathUnder(a, b) && PathUnder(b, a)
}

func (b *PolicyBuilder) denyRead() []string {
	deny := make([]string, 0, len(credentialDirNames)+1)
	for _, name := range credentialDirNames {
		deny = append(deny, filepath.Join(b.homeDir, filepath.FromSlash(name)))
	}
	// The whole app-data tree, not just data/: rewriting prefs or keys
	// here would let the sandbox lift its own constraints. Validate
	// rejects a workspace that sits inside this path.
	if b.appDataDir != "" && b.appDataDir != string(filepath.Separator) {
		deny = append(deny, b.appDataDir)
	}
	return deny
}

// Grant is one approved escalation.
type Grant struct {
	WritePath    string
	AllowNetwork bool
}

// Relax derives a wider policy after the user approved an escalation. It never
// removes a deny-read entry: those are the only mechanism enforcing them.
func (b *PolicyBuilder) Relax(base Policy, g Grant) (Policy, error) {
	out := base
	out.WritableRoots = append([]WritableRoot(nil), base.WritableRoots...)

	if g.WritePath != "" {
		path := filepath.Clean(g.WritePath)
		if err := b.rejectBroadWorkspace(path); err != nil {
			return Policy{}, err
		}
		for _, deny := range base.DenyRead {
			if PathUnder(path, deny) {
				return Policy{}, fmt.Errorf(
					"localsandbox: %q is read-denied and cannot be granted", path)
			}
		}
		out.WritableRoots = append(out.WritableRoots, WritableRoot{Path: path})
	}
	if g.AllowNetwork {
		out.Network = NetworkUnrestricted
	}
	if err := out.Validate(); err != nil {
		return Policy{}, err
	}
	return out, nil
}
