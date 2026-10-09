package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyCommandDetectsDeletes(t *testing.T) {
	const home = "/Users/dev"
	cases := []struct {
		command   string
		deletes   bool
		dangerous bool
	}{
		{"ls -la", false, false},
		{"echo hi > a.txt", false, false},
		{"docker run --rm alpine true", false, false},
		{"git status", false, false},
		{"rm a.txt", true, false},
		{"rm -f build/*.o", true, false},
		{"rmdir empty", true, false},
		{"unlink a.txt", true, false},
		{"/bin/rm a.txt", true, false},
		{"sudo rm a.txt", true, false},
		{"env FOO=1 rm a.txt", true, false},
		{"FOO=1 rm a.txt", true, false},
		{"find . -name '*.o' | xargs rm", true, false},
		{"git status && rm -rf dist", true, false},
		{"ls; rm a.txt", true, false},
		{"git rm a.txt", true, false},
		{"git -C repo rm a.txt", true, false},
		{"git clean -fdx", true, false},
		{"find . -name '*.tmp' -delete", true, false},
		{"find . -name '*.tmp' -exec rm {} +", true, false},
		{"find . -type f -print", false, false},
		{`bash -c "rm -rf out"`, true, false},
		{`bash -c "/bin/rm -rf out"`, true, false},
		{`bash -c "find . -delete"`, true, false},
		{`sh -c "git clean -fdx"`, true, false},
		{`eval "find /path -delete"`, true, false},
		{`zsh -lc "find . -exec rm {} +"`, true, false},
		{`bash -c "echo hi"`, false, false},
		{`bash -c "git status"`, false, false},
		{`bash -c "find . -type f -print"`, false, false},
		{`\rm -rf out`, true, false},
		{"rm a.txt; bash -c 'rm -rf .'", true, false},
		{`rm a.txt && sh -c "rm -rf ~"`, true, true},
		{"rm -rf ~", true, true},
		{"rm -rf ~/", true, true},
		{"rm -r $HOME", true, true},
		{"rm -Rf /Users/dev", true, true},
		{"rm --recursive /Users/dev/", true, true},
		{"rm -rf ~/*", true, true},
		{"rm -f ~", true, false},
		{"rm -rf ~/project/dist", true, false},
	}
	for _, tc := range cases {
		got := ClassifyCommand(tc.command, home)
		require.Equal(t, tc.deletes, got.Deletes, "deletes: %s", tc.command)
		require.Equal(t, tc.dangerous, got.Dangerous, "dangerous: %s", tc.command)
	}
}

func TestDeleteSegmentsKeepsOnlyTheDelete(t *testing.T) {
	got := DeleteSegments("git status && sudo rm -f a.txt && echo ok", "/Users/dev")
	require.Equal(t, []CommandSegment{{
		Text: "rm -f a.txt", Rule: DeleteRule{Program: "rm", Flags: []string{"-f"}},
	}}, got)
}

func TestDeleteSegmentsNormalizesTheProgramName(t *testing.T) {
	got := DeleteSegments("/bin/rm a.txt", "/Users/dev")
	require.Equal(t, []CommandSegment{{Text: "rm a.txt", Rule: DeleteRule{Program: "rm"}}}, got)
}

// The rule is the shape of the delete, like a Codex prefix rule: operands
// drop out, and option order or grouping does not matter.
func TestDeleteSegmentsDeriveARuleWithoutOperands(t *testing.T) {
	cases := []struct {
		command string
		rule    string
	}{
		{"rm a.txt", "rm"},
		{"rm a.txt b.txt", "rm"},
		{"rm -rf build", "rm -f -r"},
		{"rm -fr dist", "rm -f -r"},
		{"rm -r -f out", "rm -f -r"},
		{"sudo /bin/rm -f a", "rm -f"},
		{"rm --recursive --force a", "rm --force --recursive"},
		{"rm -- -f", "rm"},
		{"rmdir -p a/b", "rmdir -p"},
		{"unlink a", "unlink"},
		{"git rm --cached a.txt", "git rm --cached"},
		{"git -C repo clean -fdx", "git clean -d -f -x"},
		{"find . -name '*.o' -delete", "find -delete"},
		{"find src -type f -exec rm {} +", "find -exec rm"},
		{"ls | xargs -n 1 rm -f", "rm -f"},
	}
	for _, tc := range cases {
		got := DeleteSegments(tc.command, "/Users/dev")
		require.Len(t, got, 1, tc.command)
		require.Equal(t, tc.rule, got[0].Rule.String(), tc.command)
	}
}

func TestDeleteRuleCoversTheSameProgramWithFewerOptions(t *testing.T) {
	rf := DeleteRule{Program: "rm", Flags: []string{"-f", "-r"}}
	require.True(t, rf.Covers(DeleteRule{Program: "rm", Flags: []string{"-f", "-r"}}))
	require.True(t, rf.Covers(DeleteRule{Program: "rm", Flags: []string{"-r"}}))
	require.True(t, rf.Covers(DeleteRule{Program: "rm"}))
	require.False(t, rf.Covers(DeleteRule{Program: "rm", Flags: []string{"-f", "-r", "-v"}}))
	require.False(t, rf.Covers(DeleteRule{Program: "rmdir"}))
	require.False(t, DeleteRule{Program: "rm"}.Covers(rf), "a plain rm must not open a recursive one")
	require.False(t, DeleteRule{}.Covers(DeleteRule{}), "an empty rule covers nothing")
}

// Without these, an approved visible rm would let a hidden delete next to it
// run unasked.
func TestDeleteSegmentsTreatsSubstitutionsAndOtherShellsAsOpaque(t *testing.T) {
	for _, command := range []string{
		"rm a.txt; echo $(rm -rf .)",
		"rm a.txt; echo `rm -rf .`",
		"rm a.txt; cat <(rm -rf .)",
		"rm a.txt; dash -c 'rm -rf .'",
		"rm a.txt; ksh -c 'rm -rf .'",
		"echo $(rm -rf .)",
	} {
		got := DeleteSegments(command, "/Users/dev")
		require.Len(t, got, 1, command)
		require.True(t, got[0].Opaque, command)
		require.Equal(t, DeleteRule{}, got[0].Rule, command)
	}
}

func TestClassifyCommandProgramCaseFollowsTheFilesystem(t *testing.T) {
	const home = "/Users/dev"
	want := caseInsensitivePaths()
	for _, command := range []string{
		"RM -rf dir",
		"/bin/RM -rf dir",
		`BASH -c "find . -delete"`,
		"FIND . -delete",
		"GIT clean -fdx",
		"SUDO RM a.txt",
		`\RM -rf out`,
		"rm.exe a.txt",
		`RM.EXE -rf dir`,
	} {
		require.Equal(t, want, ClassifyCommand(command, home).Deletes, command)
	}
	require.Equal(t, want, ClassifyCommand("RM -rf ~", home).Dangerous)
	require.False(t, ClassifyCommand("docker run --RM alpine true", home).Deletes)
	require.False(t, ClassifyCommand("git CLEAN -fdx", home).Deletes)
	require.False(t, ClassifyCommand("FIND . -DELETE", home).Deletes)
	if want {
		require.Equal(t, []CommandSegment{{Text: "rm a.txt", Rule: DeleteRule{Program: "rm"}}},
			DeleteSegments("/bin/RM a.txt", home))
		require.Equal(t, []CommandSegment{{
			Text: "rm -rf ~", Dangerous: true, Rule: DeleteRule{Program: "rm", Flags: []string{"-f", "-r"}},
		}}, DeleteSegments("RM -rf ~", home))
	} else {
		require.Empty(t, DeleteSegments("/bin/RM a.txt", home))
	}
}

func TestDeleteSegmentsKeepsAQuotedDeleteAsTheWholeCommand(t *testing.T) {
	got := DeleteSegments(`bash -c "rm -rf out"`, "/Users/dev")
	require.Equal(t, []CommandSegment{{
		Text: `bash -c "rm -rf out"`, Opaque: true,
	}}, got)
}

func TestDeleteSegmentsDoesNotLetAVisibleRmCoverAHiddenOne(t *testing.T) {
	home := "/Users/dev"
	cases := []struct {
		command   string
		dangerous bool
	}{
		{"rm a.txt; bash -c 'rm -rf .'", false},
		{`rm a.txt && sh -c "rm -rf ~"`, true},
		{`bash -c "/bin/rm -rf out"`, false},
		{`\rm -rf out`, false},
		{`bash -c "find . -delete"`, false},
		{`sh -c "git clean -fdx"`, false},
		{`eval "find /path -delete"`, false},
		{"rm a.txt; bash -c 'find . -delete'", false},
	}
	for _, tc := range cases {
		got := DeleteSegments(tc.command, home)
		require.Equal(t, []CommandSegment{{
			Text:      strings.Join(strings.Fields(tc.command), " "),
			Dangerous: tc.dangerous,
			Opaque:    true,
		}}, got, tc.command)
	}
}
