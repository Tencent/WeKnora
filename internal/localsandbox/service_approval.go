package localsandbox

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
)

var errNoApprover = errors.New("nobody can approve it in this session")

// dangerousDeleteNotice refuses a recursive delete of / or the home
// directory. No mode asks about it: neither a card nor a model may approve.
const dangerousDeleteNotice = "[sandbox] this command would delete the filesystem root or the home directory. " +
	"The agent cannot run it; if it is really needed, the user should run it in their own terminal."

// approvalTurn is one Run's approval context: who answers and how.
type approvalTurn struct {
	req      RunRequest
	mode     ApprovalMode
	approver Approver
	// actor names the approver in notices for the model.
	actor string
	root  string
}

func (s *Service) newTurn(ctx context.Context, req RunRequest, ws Workspace) approvalTurn {
	mode := s.ModeForSession(ctx, req.SessionID)
	t := approvalTurn{req: req, mode: mode, approver: req.Approver, actor: "the user", root: ws.Root}
	if mode == ModeAuto {
		t.approver, t.actor = req.Reviewer, "auto review"
	}
	return t
}

// remember reports whether a session-scoped answer may be stored. Only the
// user, in ModeAsk, can widen a session.
func (t approvalTurn) remember(d ApprovalDecision) bool {
	return t.mode == ModeAsk && d.Session
}

func (s *Service) ask(ctx context.Context, t approvalTurn, req ApprovalRequest) (ApprovalDecision, error) {
	if t.approver == nil {
		return ApprovalDecision{}, errNoApprover
	}
	req.SessionID = t.req.SessionID
	req.Spec = t.req.Spec
	req.WorkspaceRoot = t.root
	logger.Infof(ctx, "[LocalSandbox] approval requested session=%s mode=%s reason=%s command=%q",
		req.SessionID, t.mode, req.Reason, previewCommand(req.Spec.Text()))
	d, err := t.approver.Approve(ctx, req)
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] approval failed session=%s reason=%s: %v", req.SessionID, req.Reason, err)
		return ApprovalDecision{}, err
	}
	logger.Infof(ctx,
		"[LocalSandbox] approval answered session=%s mode=%s reason=%s approved=%t session_scope=%t risk=%s",
		req.SessionID, t.mode, req.Reason, d.Approved, d.Session, d.RiskLevel)
	return d, nil
}

// approveDelete asks before a delete runs. A delete outside the policy also
// needs its directory, so the same question covers both and the command runs
// once with that access.
func (s *Service) approveDelete(
	ctx context.Context, t approvalTurn, policy Policy, cwd string,
) (Policy, *RunResult, error) {
	review := t.req.Spec.Text()
	segments := DeleteSegments(review, s.builder.HomeDir())
	if len(segments) == 0 {
		return policy, nil, nil
	}
	dangerous := false
	opaque := false
	rules := make([]DeleteRule, 0, len(segments))
	for _, segment := range segments {
		dangerous = dangerous || segment.Dangerous
		opaque = opaque || segment.Opaque
		rules = append(rules, segment.Rule)
	}
	if dangerous {
		logger.Warnf(ctx, "[LocalSandbox] dangerous delete refused session=%s command=%q",
			t.req.SessionID, previewCommand(review))
		return policy, &RunResult{Exit: ExitStatus{Code: 1}, Notice: dangerousDeleteNotice}, nil
	}
	// Rules ignore operands, so a delete that reaches past cwd or empties it
	// cannot be remembered. Auto still asks for those; it only skips a
	// visible delete the sandbox already confines.
	rememberable := !opaque && t.mode == ModeAsk && DeletesStayBelow(review, cwd, s.builder.HomeDir())
	places := s.deletePlaces(review, cwd)
	targets := placePaths(places)
	proposed, needsDir := s.deleteGrant(policy, places)
	switch {
	case t.mode == ModeAuto && !opaque && s.deleteAlreadyWritable(policy, places, review, cwd):
		// Every named target is already writable, or the delete stays inside
		// cwd and names nothing outside it. A find or git clean that reaches
		// past that still asks; one aimed at / or home was refused above.
		return policy, nil, nil
	case rememberable && !needsDir && s.sessions.rulesCover(t.req.SessionID, cwd, rules):
		return policy, nil, nil
	}
	approval := ApprovalRequest{
		Cwd:          cwd,
		Reason:       ReasonDelete,
		Hidden:       opaque,
		AllowSession: rememberable,
	}
	approval.Targets, approval.TargetsComplete = describeTargets(targets)
	approval.Scripts, approval.ScriptsIncomplete = s.describeScripts(policy, review, cwd)
	if rememberable {
		approval.SessionRules = ruleLabels(rules)
	}
	if needsDir {
		approval.Proposed = &proposed
	}
	d, err := s.ask(ctx, t, approval)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return policy, nil, ctxErr
	}
	if err != nil || !d.Approved {
		return policy, &RunResult{
			Exit:   ExitStatus{Code: 1},
			Notice: refusalNotice(t.actor, "delete command", d, err),
		}, nil
	}
	if needsDir {
		relaxed, err := s.builder.Relax(policy, proposed)
		if err != nil {
			return policy, &RunResult{
				Exit:   ExitStatus{Code: 1},
				Notice: fmt.Sprintf("[sandbox] could not widen the sandbox for %s: %v", proposed.Path, err),
			}, nil
		}
		policy = relaxed
	}
	if t.remember(d) && rememberable {
		for _, rule := range rules {
			s.sessions.approveRule(t.req.SessionID, cwd, rule)
		}
		if needsDir {
			s.sessions.addGrant(t.req.SessionID, proposed)
		}
	}
	return policy, nil, nil
}

// deletePlace is one absolute path a visible delete writes.
type deletePlace struct {
	path string
	// directory is a tree walk (find, git -C). The grant is path itself.
	// An rm operand unlinks an entry, so the grant is its parent.
	directory bool
}

func (s *Service) deletePlaces(review, cwd string) []deletePlace {
	var out []deletePlace
	for _, loc := range DeleteLocations(review, s.builder.HomeDir()) {
		target := loc.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		out = append(out, deletePlace{path: filepath.Clean(target), directory: loc.Directory})
	}
	return out
}

func placePaths(places []deletePlace) []string {
	out := make([]string, len(places))
	for i, place := range places {
		out[i] = place.path
	}
	return out
}

// deleteAlreadyWritable reports whether auto can run this visible delete
// without asking. A delete that names nothing (git clean of cwd) is confined
// only when every operand stays inside cwd. A named path is confined only
// when the current policy can already write it, including a session grant.
func (s *Service) deleteAlreadyWritable(policy Policy, places []deletePlace, review, cwd string) bool {
	if len(places) == 0 {
		return DeletesStayBelow(review, cwd, s.builder.HomeDir())
	}
	guard := NewPathGuard(policy)
	for _, place := range places {
		if _, err := guard.CheckWrite(place.path); err != nil {
			return false
		}
	}
	return true
}

// deleteGrant is the directory a delete needs when it removes something the
// policy cannot write. Unlinking an entry writes its parent. A find root or
// git -C directory is itself the tree being cleaned, so the grant is that
// directory rather than the parent (which for ~/Desktop would be the home
// directory, and that grant is refused).
func (s *Service) deleteGrant(policy Policy, places []deletePlace) (Grant, bool) {
	guard := NewPathGuard(policy)
	for _, place := range places {
		if _, err := guard.CheckWrite(place.path); err == nil {
			continue
		}
		grantAt := place.path
		if !place.directory {
			grantAt = filepath.Dir(place.path)
		}
		denial := Denial{Reason: DenialPolicy, Path: grantAt}
		if g, ok := s.builder.ProposeGrant(policy, denial, ""); ok {
			return g, true
		}
	}
	return Grant{}, false
}

func ruleLabels(rules []DeleteRule) []string {
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		if label := rule.String(); label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

// maxEscalations is how many distinct directories one command may ask to
// widen. The first denial asks, the retry asks once more if it hits a
// different directory, and a further denial is returned as-is.
const maxEscalations = 2

func (s *Service) escalate(
	ctx context.Context, t approvalTurn, lockRoot string, policy Policy, cwd string, res *RunResult,
) (*RunResult, error) {
	for range maxEscalations {
		proposed, ok := s.proposeEscalation(t.mode, policy, res.Denial, cwd)
		if !ok {
			res.Notice = blockedNotice(res.Denial)
			return res, nil
		}
		approval := ApprovalRequest{
			Cwd:             cwd,
			Reason:          ReasonDenied,
			Proposed:        &proposed,
			DenialSnippet:   res.Denial.Snippet,
			FirstAttemptRan: true,
			AllowSession:    t.mode == ModeAsk,
		}
		approval.Targets, approval.TargetsComplete = describeTargets([]string{proposed.Path})
		approval.Scripts, approval.ScriptsIncomplete = s.describeScripts(policy, t.req.Spec.Text(), cwd)
		d, err := s.ask(ctx, t, approval)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil || !d.Approved {
			res.Notice = blockedNotice(res.Denial) + "\n" + refusalNotice(t.actor, "access request", d, err)
			return res, nil
		}
		grant := narrowGrant(proposed, d.Grant)
		relaxed, err := s.builder.Relax(policy, grant)
		if err != nil {
			res.Notice = fmt.Sprintf("[sandbox] could not widen the sandbox for %s: %v", grant.Path, err)
			return res, nil
		}
		if t.remember(d) {
			s.sessions.addGrant(t.req.SessionID, grant)
		}
		logger.Infof(ctx, "[LocalSandbox] re-running with grant session=%s path=%s access=%s file=%t session_scope=%t",
			t.req.SessionID, grant.Path, grant.Access, grant.File, t.remember(d))
		next, err := s.runPolicy(ctx, lockRoot, relaxed, t.req)
		if err != nil {
			return nil, err
		}
		if !next.Denial.IsDenied() {
			return next, nil
		}
		policy = relaxed
		res = next
	}
	res.Notice = blockedNotice(res.Denial)
	return res, nil
}

// proposeEscalation is what a blocked command may ask for. Auto offers the
// blocked file itself when it is one; ask offers the directory, which the
// user sees on the card before deciding.
func (s *Service) proposeEscalation(mode ApprovalMode, policy Policy, d Denial, cwd string) (Grant, bool) {
	if mode == ModeAuto && d.IsDenied() && d.Path != "" {
		target := d.Path
		if !filepath.IsAbs(target) && cwd != "" {
			target = filepath.Join(cwd, target)
		}
		if g, ok := s.builder.ProposeFileGrant(policy, filepath.Clean(target)); ok {
			return g, true
		}
	}
	return s.builder.ProposeGrant(policy, d, cwd)
}

// narrowGrant applies the approver's choice. Only a directory write may be
// narrowed to read; the path always stays the one proposed.
func narrowGrant(proposed Grant, chosen *Grant) Grant {
	if chosen != nil && chosen.Access == AccessRead && !proposed.File {
		proposed.Access = AccessRead
	}
	return proposed
}

func blockedNotice(d Denial) string {
	if d.Path != "" {
		return fmt.Sprintf("[sandbox] blocked: %s is outside what this session may access", d.Path)
	}
	return "[sandbox] blocked by the workspace sandbox"
}

func refusalNotice(actor, what string, d ApprovalDecision, err error) string {
	switch {
	case errors.Is(err, errNoApprover):
		return fmt.Sprintf("[sandbox] this %s needs approval, and %v", what, err)
	case err != nil:
		return fmt.Sprintf("[sandbox] the approval request for this %s failed: %v", what, err)
	case d.Reason != "":
		return fmt.Sprintf("[sandbox] %s denied this %s: %q.", actor, what, d.Reason)
	default:
		return fmt.Sprintf("[sandbox] %s denied this %s.", actor, what)
	}
}
