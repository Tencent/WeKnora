package core

import (
	"errors"
	"fmt"
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
// Both of the other two need machinery above the sandbox that is phase 2:
// ModeAsk compiles to a valid policy with nothing writable and relies on an
// approval round-trip to widen it per command, and ModeFull is by definition
// the absence of a policy.
func (m ApprovalMode) Shipped() bool {
	return m == ModeAuto
}

// ParseApprovalMode normalizes a stored preference.
//
// This is the lenient input boundary: the value comes from a file the user can
// hand-edit, and a typo there must not stop the agent from running. Anything
// unknown or not yet shipped becomes ModeAuto, which is the only mode that
// currently runs. Enforcement is strict elsewhere — Service refuses a mode it
// cannot honour rather than substituting one.
func ParseApprovalMode(raw string) ApprovalMode {
	mode := ApprovalMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode.Shipped() {
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
	".netrc", ".git-credentials", ".pypirc", ".password-store",
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
	".asdf", ".bun", ".cache", ".cargo", ".deno", ".gem", ".gradle",
	".local", ".npm", ".nvm", ".pnpm-store", ".pyenv", ".rbenv", ".rustup",
	".sdkman", ".volta", ".yarn", "go",
	"Library/Caches",
}

// shellStartupNames are the files a login shell reads. Service runs commands
// through `bash -lc`, and this is where per-user toolchains put themselves on
// PATH, so denying them would undo homeReadableNames.
var shellStartupNames = []string{
	".profile", ".bash_profile", ".bashrc", ".zshenv", ".zprofile",
	".zshrc", ".zlogin", ".inputrc",
}

// platformReadRoots are extra readable paths for Windows / PathGuard.
// Darwin Seatbelt ignores them for availability: the base profile already
// grants unfiltered file-read* (see spec 6.1). Keeping the list makes the
// Policy fingerprint comparable across platforms.
var platformReadRoots = []string{
	"/usr/lib", "/usr/share", "/usr/bin", "/bin", "/sbin", "/usr/sbin",
	"/usr/libexec", "/System", "/Library/Apple", "/private/etc",
	"/opt/homebrew", "/usr/local",
}

type PolicyBuilder struct {
	homeDir    string
	appDataDir string
}

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

	p := Policy{
		Network:       NetworkDenied,
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
		// Nothing is writable up front; approved commands are re-prepared
		// through Relax. Cwd stays the workspace so the process has a home.
		p.WritableRoots = nil
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
	if b.homeDir == "" || b.homeDir == string(filepath.Separator) {
		return nil
	}
	return []string{b.homeDir}
}

func (b *PolicyBuilder) readableRoots(ws Workspace) []string {
	roots := append([]string(nil), platformReadRoots...)
	roots = append(roots, ws.Root)
	for _, name := range homeReadableNames {
		roots = append(roots, filepath.Join(b.homeDir, filepath.FromSlash(name)))
	}
	for _, name := range shellStartupNames {
		roots = append(roots, filepath.Join(b.homeDir, name))
	}
	return roots
}

func (b *PolicyBuilder) denyRead() []string {
	deny := make([]string, 0, len(credentialDirNames)+1)
	for _, name := range credentialDirNames {
		deny = append(deny, filepath.Join(b.homeDir, filepath.FromSlash(name)))
	}
	// WeKnora's database, stored files and signing key. Deny this subtree
	// rather than the whole app data directory: a deny-read entry covering a
	// writable root is rejected by Validate.
	deny = append(deny, filepath.Join(b.appDataDir, "data"))
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
