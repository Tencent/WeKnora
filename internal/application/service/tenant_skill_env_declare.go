package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// maxSkillEnvDeclarations bounds one skill's declaration. An agent that
	// lists a hundred variables has misunderstood the task, and every entry
	// becomes a form field somebody is asked to fill in.
	maxSkillEnvDeclarations = 20

	// maxSkillEnvDeclarationCandidates bounds bundle-scan cost, not correctness.
	// The accepted-output cap remains independent so hallucinated names cannot
	// shadow a later valid declaration.
	maxSkillEnvDeclarationCandidates = 200

	// MaxEnvValueBytes bounds one stored value. It is generous enough for a
	// PEM-encoded key and far short of anything that belongs in a file.
	MaxEnvValueBytes = 8 * 1024

	// MaxUserEnvVarsPerScope bounds how many variables one principal may keep
	// in one scope: a sandbox config, or one skill on it.
	MaxUserEnvVarsPerScope = 50
)

// envNamePattern is the first validation layer. It rejects what an LLM
// improvises: prose ("my key"), lowercase identifiers copied out of code, and
// names long enough to be a paragraph.
var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// reservedEnvNames is the third layer. It is NOT a security boundary — a skill
// can already run arbitrary code in its sandbox, and values are injected only
// into the one execution that asked for them. It is a guard against a
// self-inflicted, undiagnosable failure: a user who fills in PATH displaces
// the python inside the skill's own venv.
var reservedEnvNames = map[string]bool{
	"PATH":            true,
	"HOME":            true,
	"USER":            true,
	"SHELL":           true,
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"PYTHONPATH":      true,
	"PYTHONHOME":      true,
	"NODE_OPTIONS":    true,
}

func init() {
	// The names skill environment preparation actually writes, not a guessed prefix. Output
	// dir, history root and skill dir currently share WEKNORA_SKILL_, but
	// SESSION_INPUT_DIR does not; pulling the list from skills keeps the
	// blacklist aligned when a fifth injected name appears.
	for _, name := range skills.InjectedSandboxEnvVars() {
		reservedEnvNames[name] = true
	}
}

// reservedEnvPrefix covers future WEKNORA_SKILL_* names the sandbox may start
// injecting before the exact list above is updated. Credential names a skill
// reads — WEKNORA_API_KEY, WEKNORA_BASE_URL, WEKNORA_HOST — are outside it.
const reservedEnvPrefix = "WEKNORA_SKILL_"

// declaredSkillEnv is one entry as the installer agent wrote it.
//
// Value exists only so that an agent which ignored the prompt and wrote a
// value produces a parseable file whose value is then discarded — rather than
// a file that fails to decode and takes every legitimate entry with it. No
// declaration ever carries a value out of validateEnvDeclarations.
type declaredSkillEnv struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Value       string `json:"value,omitempty"`
}

type skillEnvDeclarationFile struct {
	Env []declaredSkillEnv `json:"env"`
}

// parseEnvDeclaration decodes the file the agent left in the sandbox. Its
// error is the caller's signal to keep the declaration empty and carry on:
// a chatty model that wrote a sentence instead of JSON must not fail an
// otherwise complete install.
func parseEnvDeclaration(raw []byte) ([]declaredSkillEnv, error) {
	var encoded struct {
		Env json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("parse skill env declaration: %w", err)
	}
	if encoded.Env == nil || bytes.Equal(bytes.TrimSpace(encoded.Env), []byte("null")) {
		return nil, fmt.Errorf("parse skill env declaration: env must be an explicit JSON array")
	}

	var declared []declaredSkillEnv
	if err := json.Unmarshal(encoded.Env, &declared); err != nil {
		return nil, fmt.Errorf("parse skill env declaration: env must be an array: %w", err)
	}
	if len(declared) > maxSkillEnvDeclarationCandidates {
		return nil, fmt.Errorf(
			"parse skill env declaration: too many entries (%d exceeds %d)",
			len(declared), maxSkillEnvDeclarationCandidates,
		)
	}
	return declared, nil
}

// validateEnvDeclarations applies the three layers in order — format, then
// the name must appear literally somewhere in the uploaded bundle, then the
// reserved list — and returns what survived.
//
// A rejected entry is dropped alone. One hallucinated name is not a reason to
// discard the real ones beside it, and the whole point of the bundle match is
// that hallucinated names are expected.
func validateEnvDeclarations(declared []declaredSkillEnv, bundle *SkillBundle) types.SkillEnvVars {
	var out types.SkillEnvVars
	seen := map[string]bool{}
	for _, entry := range declared {
		if len(out) >= maxSkillEnvDeclarations {
			break
		}
		name := strings.TrimSpace(entry.Name)
		if seen[name] {
			continue
		}
		if err := validateEnvNameFormat(name); err != nil {
			continue
		}
		if !bundleMentionsEnvName(bundle, name) {
			continue
		}
		if err := validateEnvNameNotReserved(name); err != nil {
			continue
		}
		seen[name] = true
		// Value is deliberately not copied: a declaration is metadata.
		out = append(out, types.SkillEnvVar{
			Name:        name,
			Description: strings.TrimSpace(entry.Description),
			Required:    entry.Required,
		})
	}
	return out
}

// validateUserEnvName is the format and reserved-name check, without the
// bundle match. The two are separate because a user typing a name into the
// settings page must pass these layers but not the match: matching exists to
// catch a model's invention, and a user filling a form is making a choice.
func validateUserEnvName(name string) error {
	if err := validateEnvNameFormat(name); err != nil {
		return err
	}
	return validateEnvNameNotReserved(name)
}

func validateEnvNameFormat(name string) error {
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf(
			"environment variable name %q must be UPPER_SNAKE_CASE and at most 128 characters",
			name,
		)
	}
	return nil
}

func validateEnvNameNotReserved(name string) error {
	if reservedEnvNames[name] || strings.HasPrefix(name, reservedEnvPrefix) {
		return fmt.Errorf("environment variable name %q is reserved by the sandbox", name)
	}
	return nil
}

// bundleMentionsEnvName is the layer that removes hallucinations. It searches
// every file of the bundle, not just SKILL.md: the place a variable a skill
// actually reads appears is the script that reads it.
func bundleMentionsEnvName(bundle *SkillBundle, name string) bool {
	if bundle == nil {
		return false
	}
	needle := []byte(name)
	for _, content := range bundle.Files {
		if bytes.Contains(content, needle) {
			return true
		}
	}
	return false
}

// mergeEnvDeclaration folds a fresh declaration onto the stored one.
//
// The declaration itself — which variables exist, what they are for, whether
// they are required — is replaced wholesale, because it is a statement about
// the version being installed: keeping a variable the new bundle no longer
// reads would ask users forever for something nothing consumes. The admin's
// value is carried across by name, because that is a credential a person
// typed and re-installing a skill is not a request to delete it.
func mergeEnvDeclaration(previous, declared types.SkillEnvVars) types.SkillEnvVars {
	if len(declared) == 0 {
		return nil
	}
	out := make(types.SkillEnvVars, 0, len(declared))
	for _, entry := range declared {
		if old, ok := previous.Get(entry.Name); ok && old.Value != "" {
			entry.Value = old.Value
		}
		out = append(out, entry)
	}
	return out
}
