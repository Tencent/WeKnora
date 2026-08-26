package service

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// skillPythonVerifier is the source of the in-sandbox Python checker. It is
// shipped as a file rather than a Go string literal so it can be linted and
// read as Python; the install path base64-encodes it into one shell command.
//
//go:embed tenant_skill_verify.py
var skillPythonVerifier string

// verifySkill is the server's own check, and the last gate before the image
// pointer moves: a broken install must leave the previous snapshot serving.
//
// It deliberately proves loadability rather than behaviour. Nothing in the
// runtime designates an entry point — the model names any script it likes in
// execute_skill_script — so there is no single command whose success would
// stand in for the skill working. What every one of those calls does need is
// that the interpreter can parse the file and resolve its imports inside this
// image, and that is checkable for all scripts at once without executing a
// line of the skill's own code.
//
// Not executing is the point, not a limitation. The previous implementation
// ran one guessed script with --help; skills that do not parse arguments
// simply ran their whole main path inside the tree about to be snapshotted.
func (s *TenantSkillService) verifySkill(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if err := s.verifySkillTree(ctx, mgr, sessionID, skillDir, bundle); err != nil {
		return err
	}
	if err := s.verifyDeclaredDependencies(ctx, mgr, sessionID, skillDir, bundle); err != nil {
		return err
	}
	return s.verifyScriptsLoad(ctx, mgr, sessionID, skillDir, bundle)
}

// verifySkillTree confirms the files the agent was given are still the files
// the image carries. The agent has a root shell in this directory, so "we
// wrote it before the agent ran" is not evidence that it survived.
func (s *TenantSkillService) verifySkillTree(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if _, err := s.execInstall(ctx, mgr, sessionID,
		fmt.Sprintf("test -f %s", sandbox.ShellQuote(path.Join(skillDir, "SKILL.md")))); err != nil {
		return fmt.Errorf("skill directory is incomplete after install: %w", err)
	}
	for _, rel := range sortedScriptPaths(bundle, allScriptExtensions...) {
		target := path.Join(skillDir, rel)
		if _, err := s.execInstall(ctx, mgr, sessionID,
			fmt.Sprintf("test -f %s", sandbox.ShellQuote(target))); err != nil {
			return fmt.Errorf("script %s is missing after install: %w", rel, err)
		}
	}
	return nil
}

// verifyDeclaredDependencies checks that the isolated trees the installer was
// told to create actually exist. Seeded source files surviving is not evidence
// that pip/npm ran: those files were written server-side before the agent.
//
// This is only the shape of the install. Whether the individual packages
// landed is checked by the language passes below, which read the manifests.
func (s *TenantSkillService) verifyDeclaredDependencies(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if bundleHasPythonDeps(bundle) {
		venvPython := path.Join(skillDir, ".venv", "bin", "python")
		if _, err := s.execInstall(ctx, mgr, sessionID,
			fmt.Sprintf("test -x %s", sandbox.ShellQuote(venvPython))); err != nil {
			return fmt.Errorf("python dependencies were not installed into %s/.venv: %w", skillDir, err)
		}
	}
	if bundleHasNodeDeps(bundle) {
		nodeModules := path.Join(skillDir, "node_modules")
		if _, err := s.execInstall(ctx, mgr, sessionID,
			fmt.Sprintf("test -d %s", sandbox.ShellQuote(nodeModules))); err != nil {
			return fmt.Errorf("node dependencies were not installed into %s/node_modules: %w", skillDir, err)
		}
	}
	return nil
}

// verifyScriptsLoad runs one pass per language present in the bundle. Each
// pass covers every script of that language, because every one of them is a
// script the model may name at runtime.
func (s *TenantSkillService) verifyScriptsLoad(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if scripts := sortedScriptPaths(bundle, ".py"); len(scripts) > 0 {
		if err := s.execVerify(ctx, mgr, sessionID, skillDir, "python",
			skillPythonVerifyCommand(skillDir, scripts)); err != nil {
			return err
		}
	}
	if scripts := sortedScriptPaths(bundle, ".js", ".mjs"); len(scripts) > 0 {
		if err := s.execVerify(ctx, mgr, sessionID, skillDir, "node",
			skillNodeVerifyCommand(skillDir, scripts, nodeDependencyNames(bundle))); err != nil {
			return err
		}
	}
	if scripts := sortedScriptPaths(bundle, ".sh"); len(scripts) > 0 {
		if err := s.execVerify(ctx, mgr, sessionID, skillDir, "shell",
			skillShellVerifyCommand(skillDir, scripts)); err != nil {
			return err
		}
	}
	return nil
}

// execVerify runs one verification pass as the ordinary sandbox user, with the
// same working directory and environment a real skill call gets. Running it as
// install-mode root would test permissions that never reach a session.
func (s *TenantSkillService) execVerify(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir, label, command string,
) error {
	executor, err := installExecutor(mgr)
	if err != nil {
		return err
	}
	res, err := executor.ExecShellCommandWithOptions(ctx, sessionID, command,
		sandbox.ShellExecOptions{
			WorkDir: sandbox.SessionWorkspaceRoot,
			Timeout: installCommandTimeout,
			Env: map[string]string{
				"WEKNORA_SKILL_DIR":        skillDir,
				"WEKNORA_SKILL_OUTPUT_DIR": sandbox.SessionOutputRoot,
			},
		})
	if err != nil {
		return fmt.Errorf("%s verification: %w", label, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s verification failed (%s)", label, describeExecFailure(res))
	}
	return nil
}

// skillPythonVerifyCommand pipes the verifier into the same interpreter a
// runtime skill call would use, and names the files to check on the command
// line. The list is explicit rather than a directory walk so the pass covers
// exactly the uploaded sources — never .venv, node_modules or anything else
// the agent created while installing.
func skillPythonVerifyCommand(skillDir string, scripts []string) string {
	venv := path.Join(skillDir, ".venv", "bin", "python")
	quotedVenv := sandbox.ShellQuote(venv)
	args := []string{sandbox.ShellQuote(skillDir)}
	for _, rel := range scripts {
		args = append(args, sandbox.ShellQuote(rel))
	}
	// The script arrives base64-encoded on stdin: it contains quotes, newlines
	// and backslashes, and a heredoc would still be at the mercy of whatever
	// the remote exec layer does with multi-line command strings.
	return fmt.Sprintf(
		"if [ -x %s ]; then py=%s; else py=python3; fi; printf %%s '%s' | base64 -d | \"$py\" - %s",
		quotedVenv, quotedVenv,
		base64.StdEncoding.EncodeToString([]byte(skillPythonVerifier)),
		strings.Join(args, " "),
	)
}

// skillNodeVerifyCommand parses every JavaScript file and, when the bundle
// declares runtime dependencies, checks each one resolves. `node --check` is
// parse-only: it never runs the module body.
func skillNodeVerifyCommand(skillDir string, scripts, deps []string) string {
	parts := make([]string, 0, 2)
	if len(deps) > 0 {
		quoted := make([]string, 0, len(deps))
		for _, dep := range deps {
			quoted = append(quoted, sandbox.ShellQuote(dep))
		}
		parts = append(parts, fmt.Sprintf(
			"for d in %s; do [ -e %s/\"$d\" ] || "+
				"{ echo \"package.json declares $d but node_modules/$d is missing\" >&2; exit 1; }; done",
			strings.Join(quoted, " "), sandbox.ShellQuote(path.Join(skillDir, "node_modules")),
		))
	}
	parts = append(parts, forEachScript(skillDir, scripts, "node --check \"$f\""))
	return strings.Join(parts, "; ")
}

// skillShellVerifyCommand parses every shell script. `sh -n` reads the file
// and exits without executing any of it.
func skillShellVerifyCommand(skillDir string, scripts []string) string {
	return forEachScript(skillDir, scripts, "sh -n \"$f\"")
}

func forEachScript(skillDir string, scripts []string, check string) string {
	quoted := make([]string, 0, len(scripts))
	for _, rel := range scripts {
		quoted = append(quoted, sandbox.ShellQuote(path.Join(skillDir, rel)))
	}
	return fmt.Sprintf("for f in %s; do %s || exit 1; done", strings.Join(quoted, " "), check)
}

// allScriptExtensions is every suffix the runtime knows how to execute, which
// is what makes a bundle file a script the model can name.
var allScriptExtensions = []string{".py", ".js", ".mjs", ".sh"}

// sortedScriptPaths returns the bundle's files with any of the given suffixes,
// in a stable order so the emitted command is deterministic.
func sortedScriptPaths(bundle *SkillBundle, suffixes ...string) []string {
	if bundle == nil {
		return nil
	}
	var matches []string
	for rel := range bundle.Files {
		for _, suffix := range suffixes {
			if strings.HasSuffix(rel, suffix) {
				matches = append(matches, rel)
				break
			}
		}
	}
	sort.Strings(matches)
	return matches
}

// nodeDependencyNames lists the runtime dependencies package.json declares.
// devDependencies are excluded: they are a build-time concern and the image
// is not asked to carry them.
func nodeDependencyNames(bundle *SkillBundle) []string {
	if bundle == nil {
		return nil
	}
	raw, ok := bundle.Files["package.json"]
	if !ok {
		return nil
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		// A package.json the installer could not read is the agent's problem
		// to report, not a reason to fail verification on a parse we only
		// needed for a nicer error message.
		return nil
	}
	names := make([]string, 0, len(manifest.Dependencies))
	for name := range manifest.Dependencies {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func bundleHasPythonDeps(bundle *SkillBundle) bool {
	if bundle == nil {
		return false
	}
	_, req := bundle.Files["requirements.txt"]
	_, pyproject := bundle.Files["pyproject.toml"]
	return req || pyproject
}

func bundleHasNodeDeps(bundle *SkillBundle) bool {
	if bundle == nil {
		return false
	}
	_, ok := bundle.Files["package.json"]
	return ok
}
