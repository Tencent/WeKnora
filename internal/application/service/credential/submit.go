package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// PromptGate is the part of the approval gate a submission needs.
type PromptGate interface {
	SecureFormPrompt(ctx context.Context, promptID string) (*types.AgentPrompt, error)
	ResolveSecureForm(ctx context.Context, req hitl.ResolveRequest) (hitl.Resolution, error)
}

const (
	ActionSubmit  = "submit"
	ActionDecline = "decline"

	ChoiceUseOnce = "use_once"
	ChoiceSave    = "save"
	ChoiceSaved   = "saved"
)

// SubmitRequest is what the card posts.
type SubmitRequest struct {
	PromptID    string
	Action      string
	Choice      string
	VaultItemID string
	Values      map[string]string
}

// SubmitResult reports a recorded submission.
type SubmitResult struct {
	Resolution hitl.Resolution
	// SavedButClosed: the login was saved, but the prompt had already ended,
	// so no run received it.
	SavedButClosed bool
}

// Submitter turns a card submission into a handle and the prompt's answer.
type Submitter struct {
	gate    PromptGate
	vault   *Vault
	handles *HandleStore
}

// NewSubmitter returns nil when any dependency is missing.
func NewSubmitter(gate PromptGate, vault *Vault, handles *HandleStore) *Submitter {
	if gate == nil || vault == nil || handles == nil {
		return nil
	}
	return &Submitter{gate: gate, vault: vault, handles: handles}
}

type submitDraft struct {
	domains     []string
	vaultItemID string
	fields      []types.SecretField
	username    string
}

func (d submitDraft) names() []string {
	names := make([]string, 0, len(d.fields))
	for _, f := range d.fields {
		names = append(names, f.Name)
	}
	return names
}

// Submit records req for owner. Error messages name fields, never values.
func (s *Submitter) Submit(ctx context.Context, owner Owner, req SubmitRequest) (SubmitResult, error) {
	if !Available() {
		return SubmitResult{}, errUnavailable()
	}
	prompt, err := s.gate.SecureFormPrompt(ctx, req.PromptID)
	if err != nil {
		return SubmitResult{}, err
	}
	if prompt == nil || prompt.Kind != types.AgentPromptKindSecureForm {
		return SubmitResult{}, hitl.ErrPromptNotFound
	}
	if prompt.TenantID != owner.TenantID {
		return SubmitResult{}, hitl.ErrTenantMismatch
	}
	if prompt.UserID == "" || prompt.UserID != owner.UserID {
		return SubmitResult{}, hitl.ErrUserMismatch
	}
	if !prompt.IsOpen() {
		return SubmitResult{}, hitl.ErrAlreadyResolved
	}
	var form types.SecureFormPromptRequest
	if err := json.Unmarshal(prompt.Request, &form); err != nil {
		return SubmitResult{}, fmt.Errorf("read secure form request: %w", err)
	}
	switch req.Action {
	case ActionDecline:
		return s.resolve(ctx, owner, prompt, types.SecureFormPromptResponse{Declined: true}, "", false)
	case ActionSubmit:
	default:
		return SubmitResult{}, apperrors.NewBadRequestError(`action must be "submit" or "decline"`)
	}
	draft, err := s.prepare(ctx, owner, &form, req)
	if err != nil {
		return SubmitResult{}, err
	}
	handle, err := s.handles.Create(ctx, NewHandle{
		Owner: owner, SessionID: prompt.SessionID, Domains: draft.domains,
		VaultItemID: draft.vaultItemID, Fields: draft.fields,
	})
	if err != nil {
		return SubmitResult{SavedButClosed: req.Choice == ChoiceSave}, err
	}
	resp := types.SecureFormPromptResponse{
		Handle: handle, Fields: draft.names(), Username: draft.username, Saved: req.Choice != ChoiceUseOnce,
	}
	return s.resolve(ctx, owner, prompt, resp, handle, req.Choice == ChoiceSave)
}

func (s *Submitter) resolve(
	ctx context.Context, owner Owner, prompt *types.AgentPrompt, resp types.SecureFormPromptResponse,
	handle string, saved bool,
) (SubmitResult, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return SubmitResult{}, err
	}
	res, err := s.gate.ResolveSecureForm(ctx, hitl.ResolveRequest{
		TenantID: owner.TenantID, UserID: owner.UserID, SessionID: prompt.SessionID,
		Kind: types.AgentPromptKindSecureForm, PromptID: prompt.ID, Response: payload,
	})
	if err != nil {
		if handle != "" {
			_ = s.handles.Discard(context.WithoutCancel(ctx), prompt.SessionID, handle)
		}
		return SubmitResult{SavedButClosed: saved}, err
	}
	return SubmitResult{Resolution: res}, nil
}

func (s *Submitter) prepare(
	ctx context.Context, owner Owner, form *types.SecureFormPromptRequest, req SubmitRequest,
) (submitDraft, error) {
	allowed := map[string]bool{}
	switch req.Choice {
	case ChoiceUseOnce:
		for _, n := range form.FieldNames() {
			allowed[n] = true
		}
	case ChoiceSave:
		if !form.AllowsSave() {
			return submitDraft{}, apperrors.NewBadRequestError("this form cannot be saved")
		}
		for _, n := range form.FieldNames() {
			allowed[n] = true
		}
	case ChoiceSaved:
		if form.Kind != types.SecureFormKindLogin {
			return submitDraft{}, apperrors.NewBadRequestError("this form has no login to choose")
		}
		for _, n := range form.ExtraNames() {
			allowed[n] = true
		}
	default:
		return submitDraft{}, apperrors.NewBadRequestError(`choice must be "use_once", "save" or "saved"`)
	}
	for name, value := range req.Values {
		if !allowed[name] {
			return submitDraft{}, apperrors.NewBadRequestError(fmt.Sprintf("field %q is not part of this form", name))
		}
		if len(value) > types.SecureFormMaxValueBytes {
			return submitDraft{}, apperrors.NewBadRequestError(
				fmt.Sprintf("field %q exceeds %d bytes", name, types.SecureFormMaxValueBytes))
		}
	}
	for name := range allowed {
		if form.FieldRequired(name) && strings.TrimSpace(req.Values[name]) == "" {
			return submitDraft{}, apperrors.NewBadRequestError(fmt.Sprintf("field %q is required", name))
		}
	}

	d := submitDraft{domains: form.Domains}
	username := strings.TrimSpace(req.Values[types.SecureFormFieldUsername])
	switch req.Choice {
	case ChoiceSave:
		item, err := s.vault.Upsert(ctx, owner, LoginInput{
			Site: form.Site, Domains: form.Domains, Username: username,
			Password: req.Values[types.SecureFormFieldPassword],
		})
		if err != nil {
			return submitDraft{}, err
		}
		d.vaultItemID = item.ID
		d.domains = types.IntersectDomains([]string(item.Domains), form.Domains)
	case ChoiceSaved:
		item, err := s.vault.Get(ctx, owner, req.VaultItemID)
		if err != nil {
			return submitDraft{}, err
		}
		if item.Site != form.Site {
			return submitDraft{}, apperrors.NewBadRequestError("the saved credential belongs to another site")
		}
		d.domains = types.IntersectDomains([]string(item.Domains), form.Domains)
		if len(d.domains) == 0 {
			return submitDraft{}, apperrors.NewBadRequestError(
				"the saved credential is not allowed on any of the requested domains")
		}
		d.vaultItemID = item.ID
		username = item.Username
	}
	for _, name := range form.FieldNames() {
		typ, _ := form.FieldType(name)
		switch {
		case name == types.SecureFormFieldUsername:
			d.fields = append(d.fields, types.SecretField{Name: name, Type: typ, Value: username})
		case name == types.SecureFormFieldPassword && d.vaultItemID != "":
			d.fields = append(d.fields, types.SecretField{Name: name, Type: typ})
		default:
			if v := req.Values[name]; v != "" {
				d.fields = append(d.fields, types.SecretField{Name: name, Type: typ, Value: v})
			}
		}
	}
	if form.HasPart(types.SecureFormFieldUsername) {
		d.username = username
	}
	return d, nil
}
