package service

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// skillVerifyRepairableExit is the exit code a verification pass uses when
// every problem it found is a dependency missing from this image. It separates
// "another installer round can fix this" from "the bundle has to change", which
// is the only distinction that decides what the install flow does next.
const skillVerifyRepairableExit = 2

// skillTreeVerifyDirExit is the exit code the tree check uses when the skill
// directory itself is gone, so no file inside it can be probed. Exit 1 is the
// shared "every finding is one stderr line" code; exit 0 means everything the
// bundle names is still in place.
const skillTreeVerifyDirExit = 3

// skillVerifyNotePrefix marks a line the checker reports without refusing the
// install. Notes travel on stdout so a non-zero exit stays unambiguous.
const skillVerifyNotePrefix = "note: "

// skillVerificationError is what the gate said, kept structured because it is
// also the brief for a repair round. The gate is the only authority on what has
// to resolve in this image, so handing back its own lines is what keeps the
// installer from deriving "what this skill needs" a second time.
type skillVerificationError struct {
	// Language names the pass that failed, as the operator sees it.
	Language string
	// Repairable is true when installing a package would satisfy every line.
	Repairable bool
	// Problems are the checker's own lines, one per finding.
	Problems []string
	// Summary describes the command result itself, and is the only thing worth
	// printing when the pass died without producing lines at all.
	Summary string
}

func (e *skillVerificationError) Error() string {
	if len(e.Problems) == 0 {
		return fmt.Sprintf("%s verification failed (%s)", e.Language, e.Summary)
	}
	return fmt.Sprintf("%s verification failed: %s", e.Language, strings.Join(e.Problems, "; "))
}

// verifySkill is the server's own check, and the last gate before the image
// pointer moves: a broken install must leave the previous snapshot serving.
//
// It proves loadability, not behaviour, and not "every file can be used as
// python this_file.py". A skill ships scripts and library packages together;
// execute_skill_script names files, but those files are also imported by each
// other. The Python pass resolves each file's imports against the prefixes
// Python itself would use — the file's own directory and every ancestor up to
// the skill root — plus the venv. Guessing a single entry script, or treating
// every .py as an isolated __main__, both fail real skills.
//
// Findings are graded rather than uniformly fatal. Refusing an install costs
// the minutes of dependency work that already succeeded, so only evidence that
// the install itself is broken may do it: a file nothing the skill offers ever
// loads, or a requirement pip would have skipped, is returned as a note.
//
// Not executing is the point, not a limitation. The previous implementation
// ran one guessed script with --help; skills that do not parse arguments
// simply ran their whole main path inside the tree about to be snapshotted.
func (s *TenantSkillService) verifySkill(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) ([]string, error) {
	if err := s.verifySkillTree(ctx, mgr, sessionID, skillDir, bundle); err != nil {
		return nil, err
	}
	if err := s.verifyDeclaredDependencies(ctx, mgr, sessionID, skillDir, bundle); err != nil {
		return nil, err
	}
	return s.verifyScriptsLoad(ctx, mgr, sessionID, skillDir, bundle)
}

// verifySkillTree confirms the files the agent was given are still the files
// the image carries. The agent has a root shell in this directory, so "we
// wrote it before the agent ran" is not evidence that it survived.
//
// The whole check is one command and one round trip, however many files the
// bundle carries: a missing file costs one remote exec per call otherwise, and
// a skill with dozens of scripts was spending minutes on that round-trip tax.
// Every missing file is reported on its own stderr line and the check keeps
// going, so one round trip returns every finding instead of stopping at the
// first — an install that fails anyway may as well fail completely.
func (s *TenantSkillService) verifySkillTree(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	res, err := s.execInstall(ctx, mgr, sessionID,
		skillTreeVerifyCommand(skillDir, sortedScriptPaths(bundle, allScriptExtensions...)))
	if err == nil {
		return nil
	}
	if res != nil {
		switch res.ExitCode {
		case skillTreeVerifyDirExit:
			// The directory itself is gone; nothing inside it could be probed
			// and the command already said exactly that.
			return errors.New("skill directory is incomplete after install")
		case 1:
			// The command's own protocol: exit 1 means every finding is one
			// stderr line, and those lines are the final message. Any other
			// non-zero exit has no protocol behind it and falls through to
			// the transport wrapping below rather than promoting arbitrary
			// stderr noise to a verdict.
			if lines := verificationProblems(res.Stderr); len(lines) > 0 {
				return errors.New(strings.Join(lines, "; "))
			}
		}
	}
	return fmt.Errorf("skill tree verification failed: %w", err)
}

// skillTreeVerifyCommand folds the structural check into one command: the
// SKILL.md probe plus one test per bundled script, each missing file reported
// without stopping the rest. It cds into the skill directory and iterates
// relative paths, which makes every stderr line the final user-facing message
// — the Go side never has to reassemble one. Paths reach the command only
// through ShellQuote: a file name comes from an uploaded archive, and a
// metacharacter in one must stay a literal.
func skillTreeVerifyCommand(skillDir string, scripts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s || { echo 'skill directory is incomplete after install' >&2; exit %d; }",
		sandbox.ShellQuote(skillDir), skillTreeVerifyDirExit)
	b.WriteString("; status=0")
	b.WriteString("; [ -f 'SKILL.md' ] || { echo 'SKILL.md is missing after install' >&2; status=1; }")
	if len(scripts) > 0 {
		b.WriteString("; for f in")
		for _, rel := range scripts {
			b.WriteByte(' ')
			b.WriteString(sandbox.ShellQuote(rel))
		}
		b.WriteString(`; do [ -f "$f" ] || { echo "script $f is missing after install" >&2; status=1; }; done`)
	}
	b.WriteString("; exit $status")
	return b.String()
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
// pass covers every file of that language: syntax and imports are properties
// of the file, not of a guessed entry point.
//
// Only the Python pass takes the auxiliary split. Its findings depend on what
// the image carries, so a bundled tests/ directory can fail an install that
// works; `node --check` and `bash -n` only state whether a file parses, which
// no amount of installing changes either way.
func (s *TenantSkillService) verifyScriptsLoad(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) ([]string, error) {
	var notes []string
	if scripts := sortedScriptPaths(bundle, ".py"); len(scripts) > 0 {
		entry, auxiliary := splitAuxiliaryScripts(scripts)
		found, err := s.execVerify(ctx, mgr, sessionID, skillDir, "python",
			skillPythonVerifyCommand(skillDir, entry, auxiliary))
		notes = append(notes, found...)
		if err != nil {
			return notes, err
		}
	}
	if scripts := sortedScriptPaths(bundle, ".js", ".mjs", ".cjs"); len(scripts) > 0 {
		found, err := s.execVerify(ctx, mgr, sessionID, skillDir, "node",
			skillNodeVerifyCommand(skillDir, scripts, nodeDependencyNames(bundle)))
		notes = append(notes, found...)
		if err != nil {
			return notes, err
		}
	}
	if scripts := sortedScriptPaths(bundle, ".sh"); len(scripts) > 0 {
		found, err := s.execVerify(ctx, mgr, sessionID, skillDir, "shell",
			skillShellVerifyCommand(skillDir, scripts))
		notes = append(notes, found...)
		if err != nil {
			return notes, err
		}
	}
	return notes, nil
}

// execVerify runs one verification pass as the ordinary sandbox user, with the
// same working directory and environment a real skill call gets. Running it as
// install-mode root would test permissions that never reach a session.
func (s *TenantSkillService) execVerify(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir, label, command string,
) ([]string, error) {
	executor, err := installExecutor(mgr)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%s verification: %w", label, err)
	}
	// Notes are read even from a failed pass: an install refused for a missing
	// package may also have noticed something about a file it did not refuse
	// over, and that is exactly the context whoever reads the failure needs.
	notes := verificationNotes(res.Stdout)
	if res.ExitCode != 0 {
		return notes, &skillVerificationError{
			Language:   label,
			Repairable: res.ExitCode == skillVerifyRepairableExit,
			Problems:   verificationProblems(res.Stderr),
			Summary:    describeExecFailure(res),
		}
	}
	return notes, nil
}

// verificationNotes pulls the reported-but-not-refused findings out of stdout.
func verificationNotes(stdout string) []string {
	var notes []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, skillVerifyNotePrefix); ok {
			notes = append(notes, after)
		}
	}
	return notes
}

// verificationProblems splits a failed pass's stderr into its findings. Blank
// lines are dropped; everything else is the checker's own wording, which is
// what a repair round is given.
func verificationProblems(stderr string) []string {
	var problems []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			problems = append(problems, line)
		}
	}
	return problems
}

// skillPythonVerifyCommand pipes the verifier into the same interpreter a
// runtime skill call would use, and names the files to check on the command
// line. The list is explicit rather than a directory walk so the pass covers
// exactly the uploaded sources — never .venv, node_modules or anything else
// the agent created while installing.
//
// Auxiliary files are named after --optional. They are checked the same way,
// and what is found in them is reported rather than allowed to refuse the
// install.
func skillPythonVerifyCommand(skillDir string, entry, auxiliary []string) string {
	venv := path.Join(skillDir, ".venv", "bin", "python")
	quotedVenv := sandbox.ShellQuote(venv)
	args := []string{sandbox.ShellQuote(skillDir)}
	for _, rel := range entry {
		args = append(args, sandbox.ShellQuote(rel))
	}
	if len(auxiliary) > 0 {
		args = append(args, skillVerifyOptionalFlag)
		for _, rel := range auxiliary {
			args = append(args, sandbox.ShellQuote(rel))
		}
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
//
// A declared dependency that is missing exits with the repairable code: it is
// the one thing here another installer round can still put in place.
func skillNodeVerifyCommand(skillDir string, scripts, deps []string) string {
	parts := make([]string, 0, 2)
	if len(deps) > 0 {
		quoted := make([]string, 0, len(deps))
		for _, dep := range deps {
			quoted = append(quoted, sandbox.ShellQuote(dep))
		}
		parts = append(parts, fmt.Sprintf(
			"for d in %s; do [ -e %s/\"$d\" ] || "+
				"{ echo \"package.json declares $d but node_modules/$d is missing\" >&2; exit %d; }; done",
			strings.Join(quoted, " "), sandbox.ShellQuote(path.Join(skillDir, "node_modules")),
			skillVerifyRepairableExit,
		))
	}
	parts = append(parts, forEachScript(skillDir, scripts, "node --check \"$f\""))
	return strings.Join(parts, "; ")
}

// skillShellVerifyCommand parses every shell script with the shell that would
// run it. `-n` reads the file and exits without executing any of it.
//
// bash, not sh: skills ship `#!/bin/bash` almost exclusively, and
// SkillInterpreterCommand runs them with bash for that reason. /bin/sh is dash
// on Debian, where an array literal, `function f()`, a C-style for loop and
// process substitution are all outright syntax errors — so checking with sh
// would refuse installs over scripts that run perfectly.
func skillShellVerifyCommand(skillDir string, scripts []string) string {
	return "if command -v bash >/dev/null 2>&1; then parser=bash; else parser=sh; fi; " +
		forEachScript(skillDir, scripts, `"$parser" -n "$f"`)
}

// skillVerifyOptionalFlag separates the files whose failure refuses the install
// from the ones whose failure is only reported. It is the checker's own argv
// contract; both sides must keep using this spelling.
const skillVerifyOptionalFlag = "--optional"

// auxiliaryScriptDirs are the directory names Python and JavaScript projects
// universally use for files that ship with a project without being part of
// what it offers. A skill's tests import pytest; its examples import whatever
// they illustrate. Nothing the skill exposes loads either, so neither may
// decide whether the skill installs.
var auxiliaryScriptDirs = map[string]struct{}{
	"test": {}, "tests": {}, "testing": {}, "__tests__": {},
	"example": {}, "examples": {}, "sample": {}, "samples": {},
	"benchmark": {}, "benchmarks": {}, "fixtures": {},
	"doc": {}, "docs": {},
}

// skillAuxiliaryScript reports whether a bundled path is one of those files.
//
// The rule is the naming convention the language communities already share,
// not a list grown one skill at a time: a directory the ecosystem reserves for
// tests, examples or docs, or a file name pytest and setuptools already treat
// as theirs.
func skillAuxiliaryScript(rel string) bool {
	segments := strings.Split(path.Clean(rel), "/")
	for _, segment := range segments[:len(segments)-1] {
		if _, ok := auxiliaryScriptDirs[strings.ToLower(segment)]; ok {
			return true
		}
	}
	base := strings.ToLower(segments[len(segments)-1])
	stem := strings.TrimSuffix(base, path.Ext(base))
	return stem == "conftest" || stem == "setup" ||
		strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test")
}

// splitAuxiliaryScripts separates the files whose failure is a failed install
// from the ones whose failure is a note, preserving the caller's order.
func splitAuxiliaryScripts(scripts []string) (entry, auxiliary []string) {
	for _, rel := range scripts {
		if skillAuxiliaryScript(rel) {
			auxiliary = append(auxiliary, rel)
			continue
		}
		entry = append(entry, rel)
	}
	return entry, auxiliary
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
var allScriptExtensions = []string{".py", ".js", ".mjs", ".cjs", ".sh"}

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
