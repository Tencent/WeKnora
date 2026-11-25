package localsandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionFileGrantDoesNotFollowASwappedSymlink(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	granted := filepath.Join(other, "a.txt")
	secret := filepath.Join(other, "secret.txt")
	require.NoError(t, os.WriteFile(granted, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true, Session: true}}}
	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: granted, Mode: "overwrite", Apply: overwrite("x"), Approver: user,
	})
	require.NoError(t, err)

	require.NoError(t, os.Remove(granted))
	require.NoError(t, os.Symlink(secret, granted))

	guard, _, err := svc.GuardForSession(context.Background(), "s1")
	require.NoError(t, err)
	_, err = guard.CheckRead(secret)
	require.ErrorIs(t, err, ErrPathDenied)
}

func TestStartRefusesADeleteNobodyCanAnswer(t *testing.T) {
	for _, mode := range []ApprovalMode{ModeAsk, ModeAuto} {
		backend := &fakeBackend{}
		svc, root, other := approvalFixture(t, backend)
		require.NoError(t, svc.SetSessionMode("s1", mode))
		command := "rm " + filepath.Join(other, "a.txt")
		if mode == ModeAsk {
			command = "rm " + filepath.Join(root, "a.txt")
		}
		_, err := svc.StartForSession(context.Background(), RunRequest{
			SessionID: "s1", Command: "cd x && " + command, Spec: ReviewSpec{Command: command},
		})
		require.ErrorIs(t, err, ErrAsyncCommandRefused, string(mode))
		require.Contains(t, err.Error(), "shell_exec")
		require.Empty(t, backend.prepared, "nothing ran")
	}
}

func TestStartRefusesADangerousDeleteInEveryMode(t *testing.T) {
	for _, mode := range []ApprovalMode{ModeAsk, ModeAuto} {
		backend := &fakeBackend{}
		svc, _, _ := approvalFixture(t, backend)
		require.NoError(t, svc.SetSessionMode("s1", mode))
		_, err := svc.StartForSession(context.Background(), RunRequest{SessionID: "s1", Command: "rm -rf /"})
		require.ErrorIs(t, err, ErrAsyncCommandRefused)
		require.Empty(t, backend.prepared)
	}
}

func TestStartReadsTheReviewSpecNotTheWrapper(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	_, err := svc.StartForSession(context.Background(), RunRequest{
		SessionID: "s1", Command: `{ echo "$(rm -rf x)"; }`, Spec: ReviewSpec{Command: "make build"},
	})
	require.NoError(t, err, "code-generated wrapping is not a hidden delete")
}

func TestStartScansAProgramOnStdin(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _ := approvalFixture(t, backend)
	_, err := svc.StartForSession(context.Background(), RunRequest{
		SessionID: "s1", Command: "wrapped",
		Spec: ReviewSpec{Command: "bash", Stdin: "rm " + filepath.Join(root, "a"), StdinIsProgram: true},
	})
	require.ErrorIs(t, err, ErrAsyncCommandRefused)
}

func TestAutoRunsAWorkspaceDeleteInTheBackground(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, _ := approvalFixture(t, backend)
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	proc, err := svc.StartForSession(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(root, "a"),
	})
	require.NoError(t, err)
	_, _ = proc.Wait(context.Background())
}

func TestDataOnStdinIsNotScannedAsADelete(t *testing.T) {
	backend := &fakeBackend{}
	svc, _, _ := approvalFixture(t, backend)
	user := &fakeApprover{}
	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "cat", Spec: ReviewSpec{Command: "cat", Stdin: "rm -rf everything\n"}, Approver: user,
	})
	require.NoError(t, err)
	require.Empty(t, user.requests)
}

func TestApprovalCarriesTheWorkspaceScriptACommandRuns(t *testing.T) {
	backend := &fakeBackend{}
	svc, root, other := approvalFixture(t, backend)
	body := "import shutil; shutil.rmtree('/Users/x/Desktop')\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "x.py"), []byte(body), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}

	command := "rm " + filepath.Join(other, "a") + "; python3 x.py"
	_, err := svc.Run(context.Background(), RunRequest{SessionID: "s1", Command: command, Approver: user})
	require.NoError(t, err)
	req := user.requests[0]
	require.False(t, req.ScriptsIncomplete)
	require.Equal(t, []ScriptFact{{Path: filepath.Join(root, "x.py"), Content: body}}, req.Scripts)
}

func TestApprovalMarksAScriptItCannotShow(t *testing.T) {
	for name, content := range map[string]string{
		"binary": "\x7fELF\x00\x00",
		"large":  strings.Repeat("a", maxScriptBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			svc, root, other := approvalFixture(t, &fakeBackend{})
			require.NoError(t, os.WriteFile(filepath.Join(root, "tool"), []byte(content), 0o755))
			user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
			_, err := svc.Run(context.Background(), RunRequest{
				SessionID: "s1", Command: "rm " + filepath.Join(other, "a") + "; ./tool", Approver: user,
			})
			require.NoError(t, err)
			require.True(t, user.requests[0].ScriptsIncomplete)
			require.Empty(t, user.requests[0].Scripts)
		})
	}
}

func TestApprovalDoesNotReadAScriptOutsideTheWritableScope(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	outside := filepath.Join(other, "private.py")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(other, "a") + "; python3 " + outside, Approver: user,
	})
	require.NoError(t, err)
	require.Empty(t, user.requests[0].Scripts)
	require.True(t, user.requests[0].ScriptsIncomplete)
}

// A command may write /tmp, but the guard cannot read it back to show it.
func TestApprovalMarksAScriptInTmpAsUnseen(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	user := &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}}
	_, err := svc.Run(context.Background(), RunRequest{
		SessionID: "s1", Command: "rm " + filepath.Join(other, "a") + "; bash /tmp/x.sh", Approver: user,
	})
	require.NoError(t, err)
	require.True(t, user.requests[0].ScriptsIncomplete)
}

func TestAutoReviewerSeesTheRequestedEditNotTheFile(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	target := filepath.Join(other, "secret.md")
	require.NoError(t, os.WriteFile(target, []byte("private text\n"), 0o644))
	reviewer := &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "no"}}}
	applied := false

	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Preview: "- guess\n+ other\n", Reviewer: reviewer,
		Apply: func([]byte, bool) ([]byte, error) { applied = true; return []byte("x"), nil },
	})
	require.ErrorIs(t, err, ErrFileChangeRefused)
	require.False(t, applied, "the file is not read before approval")
	require.Equal(t, "- guess\n+ other\n", reviewer.requests[0].EditDiff)
}

func TestAutoEditThatMissesLooksLikeOneThatMatches(t *testing.T) {
	refusal := func(apply func([]byte, bool) ([]byte, error)) string {
		svc, _, other := approvalFixture(t, &fakeBackend{})
		require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
		target := filepath.Join(other, "secret.md")
		require.NoError(t, os.WriteFile(target, []byte("private text\n"), 0o644))
		_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
			SessionID: "s1", Path: target, Mode: "edit", Preview: "same", Apply: apply,
			Reviewer: &fakeApprover{decisions: []ApprovalDecision{{Approved: false, Reason: "no"}}},
		})
		require.ErrorIs(t, err, ErrFileChangeRefused)
		return strings.ReplaceAll(err.Error(), target, "")
	}
	match := refusal(func(c []byte, _ bool) ([]byte, error) { return c, nil })
	miss := refusal(func([]byte, bool) ([]byte, error) { return nil, errors.New("old_string not found") })
	require.Equal(t, match, miss)
}

func TestApprovedAutoEditThatCannotApplyWritesNothing(t *testing.T) {
	svc, _, other := approvalFixture(t, &fakeBackend{})
	require.NoError(t, svc.SetSessionMode("s1", ModeAuto))
	target := filepath.Join(other, "a.md")
	require.NoError(t, os.WriteFile(target, []byte("text"), 0o644))
	_, err := svc.AuthorizeFileChange(context.Background(), FileChangeRequest{
		SessionID: "s1", Path: target, Mode: "edit", Preview: "p",
		Reviewer: &fakeApprover{decisions: []ApprovalDecision{{Approved: true}}},
		Apply:    func([]byte, bool) ([]byte, error) { return nil, errors.New(`"text" not unique`) },
	})
	require.ErrorIs(t, err, ErrEditNotApplicable)
	require.NotContains(t, err.Error(), "text\"")
}

func TestDescribeTargetsExpandsAGlobInsteadOfCallingItMissing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}
	facts, complete := describeTargets([]string{filepath.Join(dir, "*")})
	require.True(t, complete)
	require.Equal(t, []TargetFact{
		{Path: filepath.Join(dir, "a.txt"), Type: "file", Size: 1},
		{Path: filepath.Join(dir, "b.txt"), Type: "file", Size: 1},
	}, facts)
}

func TestDescribeTargetsCountsExpandedEntriesAgainstTheLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= maxReviewTargets; i++ {
		name := filepath.Join(dir, strings.Repeat("f", i+1))
		require.NoError(t, os.WriteFile(name, nil, 0o644))
	}
	facts, complete := describeTargets([]string{filepath.Join(dir, "*")})
	require.False(t, complete)
	require.Len(t, facts, maxReviewTargets)
}

func TestDescribeTargetsTreatsBraceExpansionAsUnknown(t *testing.T) {
	_, complete := describeTargets([]string{"/x/{a,b}"})
	require.False(t, complete, "only the shell can expand this")
}

func TestDescribeTargetsReportsAGlobWithNoMatchAsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "*.none")
	facts, complete := describeTargets([]string{path})
	require.True(t, complete)
	require.Equal(t, []TargetFact{{Path: path, Type: "missing"}}, facts)
}
