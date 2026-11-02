package localsandbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Tencent/WeKnora/internal/logger"
)

// ErrFileChangeRefused is a file write the approver declined.
var ErrFileChangeRefused = errors.New("localsandbox: file change refused")

// maxFileChangeReadBytes is the most an edit or append may load before
// approval. It matches the tool write cap. Overwrite does not read the file.
const maxFileChangeReadBytes = 256 * 1024

// ErrEditNotApplicable hides why an edit to an out-of-scope file failed: the
// reason could quote content the model is not allowed to read.
var ErrEditNotApplicable = errors.New("cannot apply the edit to this file")

// FileChangeRequest is one write a file tool wants to make.
type FileChangeRequest struct {
	SessionID string
	Path      string
	Mode      string
	// Apply computes the new file from the current one. exists is false for
	// a file not yet created.
	Apply func(current []byte, exists bool) ([]byte, error)
	// Preview is an edit's requested replacements, holding nothing from the
	// file. The approver sees it whenever the file cannot be read first.
	Preview  string
	Spec     ReviewSpec
	Approver Approver
	Reviewer Approver
}

// FileApproval lets one tool call write Path with exactly the approved bytes.
type FileApproval struct {
	Grant  Grant
	Digest [32]byte
}

// AuthorizeFileChange approves one write outside the session's scope. It
// returns nil when the path is already in scope. The approver sees the content,
// diff or requested replacements, and the approval is bound by digest to the
// bytes that will be written. The wait happens outside the workspace lock.
func (s *Service) AuthorizeFileChange(ctx context.Context, req FileChangeRequest) (*FileApproval, error) {
	if req.Apply == nil || !filepath.IsAbs(req.Path) {
		return nil, fmt.Errorf("%w: %q", ErrPathDenied, req.Path)
	}
	ws, policy, err := s.workspaceFor(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	path := filepath.Clean(req.Path)
	if _, err := NewPathGuard(policy).CheckWrite(path); err == nil {
		return nil, nil
	}
	grant, ok := s.builder.ProposeFileGrant(policy, path)
	if !ok {
		return nil, fmt.Errorf("%w: %q cannot be opened for writing (it is a directory, its folder does not exist, "+
			"or it is protected)", ErrPathDenied, req.Path)
	}
	relaxed, err := s.builder.Relax(policy, grant)
	if err != nil {
		return nil, err
	}
	guard := NewPathGuard(relaxed)
	run := RunRequest{SessionID: req.SessionID, Spec: req.Spec, Approver: req.Approver, Reviewer: req.Reviewer}
	t := s.newTurn(ctx, run, ws)
	approval := ApprovalRequest{
		Cwd: ws.Root, Reason: ReasonFileWrite, Proposed: &grant, AllowSession: t.mode == ModeAsk,
		WriteMode: req.Mode,
	}
	// An edit that misses must look like one that matches until someone has
	// approved opening the file, or the model could probe contents with guesses.
	// A person may see the real diff. The review model may not: its answer and
	// its reason go back to the agent, so it only gets the requested replacements.
	var current, next []byte
	applied := false
	if req.Mode != "edit" || t.mode == ModeAsk {
		current, next, applied = s.applyFileChange(ctx, guard, grant.Path, req)
		if !applied && req.Mode != "edit" {
			return nil, ErrEditNotApplicable
		}
	}
	switch {
	case !applied:
		approval.EditDiff = req.Preview
	case req.Mode == "edit":
		approval.EditDiff = unifiedDiff(grant.Path, current, next)
		approval.WriteBytes = len(next)
	case req.Mode == "append":
		approval.WriteContent = string(next[len(current):])
		approval.WriteBytes = len(next)
	default:
		approval.WriteContent = string(next)
		approval.WriteBytes = len(next)
	}
	approval.Targets, approval.TargetsComplete = describeTargets([]string{grant.Path})
	d, askErr := s.ask(ctx, t, approval)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if askErr != nil || !d.Approved {
		return nil, fmt.Errorf("%w: %s", ErrFileChangeRefused, refusalNotice(t.actor, "file write", d, askErr))
	}
	if !applied {
		if _, next, applied = s.applyFileChange(ctx, guard, grant.Path, req); !applied {
			return nil, ErrEditNotApplicable
		}
	}
	if t.remember(d) {
		s.sessions.addGrant(req.SessionID, grant)
	}
	logger.Infof(ctx, "[LocalSandbox] file write approved session=%s path=%s mode=%s session_scope=%t",
		req.SessionID, grant.Path, req.Mode, t.remember(d))
	return &FileApproval{Grant: grant, Digest: sha256.Sum256(next)}, nil
}

// applyFileChange computes the new file. Why it failed stays in the log: the
// reason can quote the file or reveal its size.
func (s *Service) applyFileChange(
	ctx context.Context, guard *PathGuard, path string, req FileChangeRequest,
) (current, next []byte, ok bool) {
	current, exists, err := readForChange(guard, path, req.Mode)
	if err == nil {
		next, err = req.Apply(current, exists)
	}
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] file change not applicable session=%s path=%s: %v",
			req.SessionID, path, err)
		return nil, nil, false
	}
	return current, next, true
}

func readForChange(guard *PathGuard, path, mode string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if mode != "edit" && mode != "append" {
		return nil, true, nil
	}
	if info.Size() > maxFileChangeReadBytes {
		return nil, true, fmt.Errorf("file too large to %s (%d bytes; max %d)", mode, info.Size(), maxFileChangeReadBytes)
	}
	data, err := guard.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
