package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// declareBundle is the skill package every validation case is matched back
// against. The interesting name lives in the script rather than in SKILL.md,
// because that is where a variable a skill really reads shows up.
func declareBundle() *SkillBundle {
	return &SkillBundle{
		Name: "pdf-tools",
		Files: map[string][]byte{
			"SKILL.md": []byte(
				"Set DOC_ROOT before running. Do not touch PATH, path_prefix or " +
					"WEKNORA_SKILL_DIR.\n",
			),
			"scripts/extract.py": []byte(
				"import os\nkey = os.environ[\"TAVILY_API_KEY\"]\n",
			),
		},
	}
}

func TestParseEnvDeclarationReadsTheAgentsFile(t *testing.T) {
	declared, err := parseEnvDeclaration([]byte(`{"env":[
		{"name":"TAVILY_API_KEY","description":"search key","required":true},
		{"name":"DOC_ROOT"}
	]}`))

	require.NoError(t, err)
	require.Equal(t, []declaredSkillEnv{
		{Name: "TAVILY_API_KEY", Description: "search key", Required: true},
		{Name: "DOC_ROOT"},
	}, declared)
}

func TestParseEnvDeclarationAcceptsAnEmptyList(t *testing.T) {
	declared, err := parseEnvDeclaration([]byte(`{"env":[]}`))

	require.NoError(t, err)
	require.Empty(t, declared)
}

func TestParseEnvDeclarationRequiresAnExplicitArray(t *testing.T) {
	for _, raw := range []string{`{}`, `{"env":null}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseEnvDeclaration([]byte(raw))
			require.Error(t, err)
		})
	}
}

func TestParseEnvDeclarationRejectsGarbage(t *testing.T) {
	_, err := parseEnvDeclaration([]byte("I installed the skill, boss!"))
	require.Error(t, err)
}

func TestParseEnvDeclarationRejectsAWrongShape(t *testing.T) {
	_, err := parseEnvDeclaration([]byte(`{"env":"TAVILY_API_KEY"}`))
	require.Error(t, err)
}

func TestParseEnvDeclarationRejectsAnImplausiblyLargeList(t *testing.T) {
	entries := make([]declaredSkillEnv, maxSkillEnvDeclarationCandidates+1)
	for i := range entries {
		entries[i].Name = fmt.Sprintf("VAR_%03d", i)
	}
	raw, err := json.Marshal(skillEnvDeclarationFile{Env: entries})
	require.NoError(t, err)

	_, err = parseEnvDeclaration(raw)
	require.Error(t, err)
	require.ErrorContains(t, err, "too many")
}

func TestValidateUserEnvNameFormatLayer(t *testing.T) {
	require.NoError(t, validateEnvNameFormat("PATH"),
		"format validation must not accidentally include the reserved-name layer")
	require.NoError(t, validateEnvNameFormat("_PRIVATE"))
	require.Error(t, validateEnvNameFormat("path_prefix"))
	require.Error(t, validateEnvNameFormat("HAS SPACE"))
	require.Error(t, validateEnvNameFormat("1LEADING_DIGIT"))
}

func TestValidateUserEnvNameReservedLayer(t *testing.T) {
	require.NoError(t, validateEnvNameNotReserved("TAVILY_API_KEY"))
	require.NoError(t, validateEnvNameNotReserved("lowercase-is-a-format-concern"),
		"reserved-name validation must remain independent of name format")
	require.Error(t, validateEnvNameNotReserved("PATH"))
	require.Error(t, validateEnvNameNotReserved("LD_PRELOAD"))
	require.Error(t, validateEnvNameNotReserved("WEKNORA_SKILL_DIR"))
}

func TestValidateEnvDeclarationBundleMatchLayerSearchesEveryFile(t *testing.T) {
	bundle := declareBundle()
	require.True(t, bundleMentionsEnvName(bundle, "TAVILY_API_KEY"))
	require.True(t, bundleMentionsEnvName(bundle, "DOC_ROOT"))
	require.False(t, bundleMentionsEnvName(bundle, "OPENAI_API_KEY"))
}

func TestValidateEnvDeclarationsAppliesThreeLayers(t *testing.T) {
	cases := []struct {
		name     string
		declared []declaredSkillEnv
		want     types.SkillEnvVars
	}{
		{
			name:     "a name the script reads survives",
			declared: []declaredSkillEnv{{Name: "TAVILY_API_KEY", Required: true}},
			want:     types.SkillEnvVars{{Name: "TAVILY_API_KEY", Required: true}},
		},
		{
			name:     "a name only SKILL.md mentions survives",
			declared: []declaredSkillEnv{{Name: "DOC_ROOT", Description: "corpus root"}},
			want:     types.SkillEnvVars{{Name: "DOC_ROOT", Description: "corpus root"}},
		},
		{
			name:     "a name that appears nowhere in the bundle is a hallucination",
			declared: []declaredSkillEnv{{Name: "OPENAI_API_KEY"}},
			want:     nil,
		},
		{
			name:     "PATH is reserved even though the bundle mentions it",
			declared: []declaredSkillEnv{{Name: "PATH"}},
			want:     nil,
		},
		{
			name:     "the WEKNORA_ prefix is reserved",
			declared: []declaredSkillEnv{{Name: "WEKNORA_SKILL_DIR"}},
			want:     nil,
		},
		{
			name:     "a lowercase name fails the format layer",
			declared: []declaredSkillEnv{{Name: "path_prefix"}},
			want:     nil,
		},
		{
			name:     "a name with a space fails the format layer",
			declared: []declaredSkillEnv{{Name: "TAVILY API KEY"}},
			want:     nil,
		},
		{
			name: "one rejected entry does not take its neighbours with it",
			declared: []declaredSkillEnv{
				{Name: "OPENAI_API_KEY"},
				{Name: "TAVILY_API_KEY", Required: true},
				{Name: "PATH"},
				{Name: "DOC_ROOT"},
			},
			want: types.SkillEnvVars{
				{Name: "TAVILY_API_KEY", Required: true},
				{Name: "DOC_ROOT"},
			},
		},
		{
			name: "a repeated name collapses onto the first declaration",
			declared: []declaredSkillEnv{
				{Name: "DOC_ROOT", Description: "first"},
				{Name: "DOC_ROOT", Description: "second", Required: true},
			},
			want: types.SkillEnvVars{{Name: "DOC_ROOT", Description: "first"}},
		},
		{
			name:     "nothing declared yields nothing stored",
			declared: nil,
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, validateEnvDeclarations(tc.declared, declareBundle()))
		})
	}
}

func TestValidateEnvDeclarationsTruncatesARunawayList(t *testing.T) {
	bundle := declareBundle()
	var declared []declaredSkillEnv
	for i := 0; i < maxSkillEnvDeclarations+5; i++ {
		name := fmt.Sprintf("VAR_%02d", i)
		bundle.Files["scripts/extract.py"] = append(
			bundle.Files["scripts/extract.py"], []byte("os.environ["+name+"]\n")...)
		declared = append(declared, declaredSkillEnv{Name: name})
	}

	got := validateEnvDeclarations(declared, bundle)

	require.Len(t, got, maxSkillEnvDeclarations)
	require.Equal(t, "VAR_00", got[0].Name)
	require.Equal(t, "VAR_19", got[maxSkillEnvDeclarations-1].Name)
}

// A declaration is metadata. The prompt forbids values outright; parsing one
// and dropping it here is what keeps a disobedient agent from writing a
// credential of its own invention into the workspace-wide slot.
func TestValidateEnvDeclarationsNeverCarriesAValue(t *testing.T) {
	got := validateEnvDeclarations([]declaredSkillEnv{
		{Name: "TAVILY_API_KEY", Value: "tvly-guessed-by-the-model"},
	}, declareBundle())

	require.Equal(t, types.SkillEnvVars{{Name: "TAVILY_API_KEY"}}, got)
}

func TestMergeEnvDeclarationKeepsTheAdminValueByName(t *testing.T) {
	previous := types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Description: "old text", Required: false, Value: "tvly-real"},
		{Name: "GONE_IN_THE_NEW_VERSION", Value: "orphan"},
	}
	declared := types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Description: "new text", Required: true},
		{Name: "DOC_ROOT"},
	}

	got := mergeEnvDeclaration(previous, declared)

	require.Equal(t, types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Description: "new text", Required: true, Value: "tvly-real"},
		{Name: "DOC_ROOT"},
	}, got, "metadata follows the new declaration; the value an admin typed survives it")
}

func TestMergeEnvDeclarationDropsWhatTheNewVersionNoLongerReads(t *testing.T) {
	got := mergeEnvDeclaration(
		types.SkillEnvVars{{Name: "GONE", Value: "orphan"}},
		types.SkillEnvVars{{Name: "DOC_ROOT"}},
	)

	require.Equal(t, types.SkillEnvVars{{Name: "DOC_ROOT"}}, got,
		"asking a user forever for a variable nothing reads is worse than losing it")
}

func TestMergeEnvDeclarationWithoutADeclarationStoresNothing(t *testing.T) {
	require.Nil(t, mergeEnvDeclaration(types.SkillEnvVars{{Name: "OLD", Value: "v"}}, nil))
}

func TestValidateUserEnvNameAcceptsAndRejects(t *testing.T) {
	require.NoError(t, validateUserEnvName("TAVILY_API_KEY"))
	require.NoError(t, validateUserEnvName("_PRIVATE"))

	require.Error(t, validateUserEnvName(""))
	require.Error(t, validateUserEnvName("path_prefix"))
	require.Error(t, validateUserEnvName("HAS SPACE"))
	require.Error(t, validateUserEnvName("1LEADING_DIGIT"))
	require.Error(t, validateUserEnvName("PATH"))
	require.Error(t, validateUserEnvName("LD_PRELOAD"))
	require.Error(t, validateUserEnvName("WEKNORA_SKILL_DIR"))
}

// The user path deliberately skips the bundle match: matching exists to catch
// an LLM's invention, and a user filling a form is not guessing.
func TestValidateUserEnvNameDoesNotRequireTheNameToBeInAnyBundle(t *testing.T) {
	require.NoError(t, validateUserEnvName("SOMETHING_NO_SKILL_MENTIONS"))
}
