package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const (
	// skillEventPollInterval is how often a live stream re-reads the durable
	// status. It is not a nicety: a run can end without publishing anything
	// (a duplicate removal returns early), and this poll is what ends the
	// stream in that case.
	skillEventPollInterval = 5 * time.Second

	// skillEventMaxDuration bounds one stream. An install whose process died
	// leaves the row at "installing" until the stuck-run reaper rewrites it,
	// and a connection that waits for that forever is a leak, so the stream
	// stops following and says so.
	skillEventMaxDuration = 60 * time.Minute

	// skillUploadEnvelopeSlack is the multipart framing allowed on top of the
	// file itself, so the body cap refuses a genuinely oversized upload
	// without rejecting a legal one for its boundary lines.
	skillUploadEnvelopeSlack = 1 << 20
)

// sandboxSkillService is the skill surface the admin endpoints need. Reads and
// writes are workspace-scoped by the service; the progress calls are not, which
// is why every handler here resolves the skill for the caller's workspace first.
type sandboxSkillService interface {
	ListSkills(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillEntity, error)
	GetSkill(ctx context.Context, tenantID uint64, configID, skillID string) (*types.TenantSkillEntity, error)
	SetSkillEnabled(
		ctx context.Context, tenantID uint64, configID, skillID string, enabled bool,
	) (*types.TenantSkillEntity, error)
	InstallSkill(ctx context.Context, tenantID uint64, configID string, archive []byte) (string, error)
	InstallSkillFromSource(
		ctx context.Context, tenantID uint64, configID, source string,
	) (string, error)
	RemoveSkill(ctx context.Context, tenantID uint64, configID, skillID string) error
	LastProgress(
		ctx context.Context, tenantID uint64, configID, skillID string,
	) (service.SkillProgress, bool)
	SubscribeProgress(
		ctx context.Context, tenantID uint64, configID, skillID string,
	) (<-chan service.SkillProgress, func(), error)
}

// SandboxSkillHandler serves the agent-skill endpoints of one sandbox config.
type SandboxSkillHandler struct {
	service sandboxSkillService

	// streams replays the installer agent's own event log. It is the same
	// manager the install writes through, and it may be nil in a deployment
	// without Redis, which only costs the transcript endpoint.
	streams interfaces.StreamManager

	// pollInterval and maxDuration are fields rather than constants so a test
	// can exercise a whole stream lifecycle without waiting minutes for it.
	pollInterval time.Duration
	maxDuration  time.Duration
}

// NewSandboxSkillHandler constructs the admin HTTP surface for tenant skills.
func NewSandboxSkillHandler(
	svc sandboxSkillService, streams interfaces.StreamManager,
) *SandboxSkillHandler {
	return &SandboxSkillHandler{
		service:      svc,
		streams:      streams,
		pollInterval: skillEventPollInterval,
		maxDuration:  skillEventMaxDuration,
	}
}

// skillResponse is the outward projection of an installed skill. The SKILL.md
// body is deliberately omitted: it is level-2 disclosure for the agent, and a
// list of them would dwarf the response.
type skillResponse struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Version             string    `json:"version,omitempty"`
	Description         string    `json:"description,omitempty"`
	Enabled             bool      `json:"enabled"`
	Status              string    `json:"status"`
	Error               string    `json:"error,omitempty"`
	BundleSHA256        string    `json:"bundle_sha256,omitempty"`
	InstalledSnapshotID string    `json:"installed_snapshot_id,omitempty"`
	InstallSessionID    string    `json:"install_session_id,omitempty"`
	InstallMessageID    string    `json:"install_message_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func toSkillResponse(e *types.TenantSkillEntity) skillResponse {
	if e == nil {
		return skillResponse{}
	}
	return skillResponse{
		ID:                  e.ID,
		Name:                e.Name,
		Version:             e.Version,
		Description:         e.Description,
		Enabled:             e.Enabled,
		Status:              e.Status,
		Error:               e.Error,
		BundleSHA256:        e.BundleSHA256,
		InstalledSnapshotID: e.InstalledSnapshotID,
		InstallSessionID:    e.InstallSessionID,
		InstallMessageID:    e.InstallMessageID,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

// respondSkillServiceError promotes every rejection of an uploaded archive to
// 400. It is matched as a sentinel rather than by message so a reworded
// validation error cannot silently start returning 500 for bad input.
func respondSkillServiceError(c *gin.Context, err error) {
	if stderrors.Is(err, service.ErrSkillBundleInvalid) || stderrors.Is(err, service.ErrSkillSourceInvalid) {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	_ = c.Error(err)
}

// List godoc
// @Summary      List installed skills
// @Description  List the agent skills installed onto one sandbox config's image.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id   path      string  true  "Sandbox config ID"
// @Success      200  {object}  map[string]interface{}  "Installed skills"
// @Failure      401  {object}  map[string]interface{}  "Unauthorized"
// @Failure      404  {object}  apperrors.AppError      "Sandbox config not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills [get]
func (h *SandboxSkillHandler) List(c *gin.Context) {
	skills, err := h.service.ListSkills(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	data := make([]skillResponse, 0, len(skills))
	for _, skill := range skills {
		data = append(data, toSkillResponse(skill))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// Get godoc
// @Summary      Get an installed skill
// @Description  Retrieve one installed skill of a sandbox config.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id       path      string  true  "Sandbox config ID"
// @Param        skillId  path      string  true  "Skill ID"
// @Success      200      {object}  map[string]interface{}  "Installed skill"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Skill not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills/{skillId} [get]
func (h *SandboxSkillHandler) Get(c *gin.Context) {
	skill, err := h.resolveSkill(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": toSkillResponse(skill)})
}

// Upload godoc
// @Summary      Install a skill
// @Description  Install a skill onto this sandbox config's image. Send a zip
// @Description  as multipart form field "file", or JSON {"source":"..."} to
// @Description  pull from ClawHub, SkillHub, skills.sh, GitHub, GitLab, or a
// @Description  direct archive URL. The source must be readable anonymously.
// @Description  The install boots a sandbox and runs for minutes, so the
// @Description  request is only accepted; follow it via the install-events
// @Description  stream.
// @Tags         SandboxConfig
// @Accept       json
// @Accept       multipart/form-data
// @Produce      json
// @Param        id      path      string              true   "Sandbox config ID"
// @Param        file    formData  file                false  "Skill bundle (zip)"
// @Param        request body      skillSourceRequest  false  "Install from a registry, git host, or archive URL"
// @Success      202     {object}  map[string]interface{}  "Install accepted"
// @Failure      400     {object}  apperrors.AppError      "Missing, oversized or invalid bundle or source"
// @Failure      401   {object}  map[string]interface{}  "Unauthorized"
// @Failure      404   {object}  apperrors.AppError      "Sandbox config not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills [post]
func (h *SandboxSkillHandler) Upload(c *gin.Context) {
	maxBytes := secutils.GetMaxFileSize()
	// The cap is applied to the body before it is parsed: multipart parsing
	// buffers the whole request, spilling to temp files, so checking only the
	// declared part size would let an unbounded body through first.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+skillUploadEnvelopeSlack)

	if strings.HasPrefix(c.ContentType(), "application/json") {
		h.installFromSource(c)
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if stderrors.As(err, &tooLarge) {
			_ = c.Error(skillTooLargeError())
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("file is required"))
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size > maxBytes {
		_ = c.Error(skillTooLargeError())
		return
	}
	archive, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("failed to read the uploaded skill bundle"))
		return
	}
	// A multipart part may under-declare its size, so the bytes actually read
	// are checked too.
	if int64(len(archive)) > maxBytes {
		_ = c.Error(skillTooLargeError())
		return
	}

	skillID, err := h.service.InstallSkill(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), archive,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"skill_id": skillID}})
}

type skillSourceRequest struct {
	// Source is a ClawHub/SkillHub slug or page URL, a skills.sh / GitHub /
	// GitLab locator, or a direct zip/SKILL.md URL. It must be readable
	// anonymously: the fetch carries no credential of any kind.
	Source string `json:"source"`
}

func (h *SandboxSkillHandler) installFromSource(c *gin.Context) {
	var req skillSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if stderrors.As(err, &tooLarge) {
			_ = c.Error(skillTooLargeError())
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("invalid skill source request"))
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		_ = c.Error(apperrors.NewBadRequestError("source is required"))
		return
	}

	skillID, err := h.service.InstallSkillFromSource(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), source,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"skill_id": skillID}})
}

func skillTooLargeError() error {
	return apperrors.NewBadRequestError(
		fmt.Sprintf("skill bundle cannot exceed %d MB", secutils.GetMaxFileSizeMB()))
}

type skillPatchRequest struct {
	// Enabled is a pointer because its absence is a bad request rather than a
	// request to disable the skill.
	Enabled *bool `json:"enabled"`
}

// Patch godoc
// @Summary      Update an installed skill
// @Description  Show or hide an installed skill. The files stay in the image either way; removal is a separate flow.
// @Tags         SandboxConfig
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "Sandbox config ID"
// @Param        skillId  path      string             true  "Skill ID"
// @Param        request  body      skillPatchRequest  true  "Fields to update"
// @Success      200      {object}  map[string]interface{}  "Updated skill"
// @Failure      400      {object}  apperrors.AppError      "Invalid request"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Skill not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills/{skillId} [patch]
func (h *SandboxSkillHandler) Patch(c *gin.Context) {
	var req skillPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	if req.Enabled == nil {
		_ = c.Error(apperrors.NewBadRequestError("enabled is required"))
		return
	}
	updated, err := h.service.SetSkillEnabled(c.Request.Context(), sandboxConfigTenantID(c),
		c.Param("id"), c.Param("skillId"), *req.Enabled)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if updated == nil {
		_ = c.Error(apperrors.NewNotFoundError("skill not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": toSkillResponse(updated)})
}

// Delete godoc
// @Summary      Remove an installed skill
// @Description  Remove a skill from the config's image. The removal rebuilds
// @Description  the image and runs for minutes, so it is only accepted; follow
// @Description  it via the install-events stream.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id       path      string  true  "Sandbox config ID"
// @Param        skillId  path      string  true  "Skill ID"
// @Success      202      {object}  map[string]interface{}  "Removal accepted"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Skill not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills/{skillId} [delete]
func (h *SandboxSkillHandler) Delete(c *gin.Context) {
	skillID := c.Param("skillId")
	err := h.service.RemoveSkill(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), skillID,
	)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"skill_id": skillID}})
}

// skillInstallEvent is one frame of the install/removal stream. Done is carried
// explicitly so a client terminates on the flag rather than on a stage name it
// has to keep in sync with the server.
type skillInstallEvent struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Log     string `json:"log,omitempty"`
	Status  string `json:"status,omitempty"`
	Done    bool   `json:"done"`
}

// Terminal stages, as published by the service when a run reaches 100%.
const (
	skillStageDone   = "done"
	skillStageFailed = "failed"
	// skillStageDetached is the handler's own: the stream stopped following a
	// run that is still in progress. It is not a verdict on the install.
	skillStageDetached = "detached"
)

// skillStatusRemoved is the status a finished removal publishes. The row is
// gone by then, so it is also what a synthesized terminal event reports.
const skillStatusRemoved = "removed"

func skillEventFromProgress(p service.SkillProgress) skillInstallEvent {
	return skillInstallEvent{
		Percent: p.Percent,
		Stage:   p.Stage,
		Log:     p.Log,
		Status:  p.Status,
		Done:    p.Stage == skillStageDone || p.Stage == skillStageFailed,
	}
}

// terminalSkillEvent derives an end-of-stream frame from the durable state, for
// every run that ends without publishing one: a duplicate removal returns
// early, and a run whose process died publishes nothing ever again. A nil skill
// is a finished removal — the row is deleted by the last step of one.
func terminalSkillEvent(skill *types.TenantSkillEntity) (skillInstallEvent, bool) {
	if skill == nil {
		return skillInstallEvent{
			Percent: 100, Stage: skillStageDone, Status: skillStatusRemoved, Done: true,
		}, true
	}
	switch skill.Status {
	case types.SkillStatusInstalling, types.SkillStatusRemoving:
		return skillInstallEvent{}, false
	case types.SkillStatusFailed:
		return skillInstallEvent{
			Percent: 100, Stage: skillStageFailed, Status: skill.Status,
			Log: skill.Error, Done: true,
		}, true
	default:
		return skillInstallEvent{
			Percent: 100, Stage: skillStageDone, Status: skill.Status,
			Log: skill.Error, Done: true,
		}, true
	}
}

// InstallEvents godoc
// @Summary      Follow an install or removal
// @Description  Server-sent progress for one install or removal. The stream
// @Description  always terminates: with the run's own terminal event, with one
// @Description  derived from the durable status, or with a "detached" frame
// @Description  when it stops following a run that is still going.
// @Tags         SandboxConfig
// @Produce      text/event-stream
// @Param        id       path      string  true  "Sandbox config ID"
// @Param        skillId  path      string  true  "Skill ID"
// @Success      200      {string}  string  "SSE stream of progress events"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Skill not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills/{skillId}/install-events [get]
func (h *SandboxSkillHandler) InstallEvents(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := sandboxConfigTenantID(c)
	configID, skillID := c.Param("id"), c.Param("skillId")

	// The progress key is workspace-scoped, so this lookup is not what isolates
	// the stream; it is what turns "this skill is not yours" into a 404 while a
	// refusal can still be rendered as JSON, before any SSE header is written.
	skill, err := h.resolveSkill(c)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Subscribing before the first read means an event published between the
	// two is delivered rather than missed.
	events, release, err := h.service.SubscribeProgress(ctx, tenantID, configID, skillID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	defer release()

	setSandboxSkillSSEHeaders(c)

	// lastPercent is what a detached frame reports, so giving up on following
	// a run does not appear to reset its progress.
	lastPercent := 0
	if last, ok := h.service.LastProgress(ctx, tenantID, configID, skillID); ok {
		event := skillEventFromProgress(last)
		lastPercent = event.Percent
		if !h.emit(c, event) || event.Done {
			return
		}
	}
	if terminal, ok := terminalSkillEvent(skill); ok {
		h.emit(c, terminal)
		return
	}
	if events == nil {
		// Nothing publishes progress without Redis. One frame stating the
		// durable status is all this connection can ever say.
		h.emit(c, skillInstallEvent{
			Stage:  skill.Status,
			Status: skill.Status,
			Log:    "live progress is unavailable; poll the skill for its status",
			Done:   true,
		})
		return
	}

	poll := time.NewTicker(h.pollInterval)
	defer poll.Stop()
	deadline := time.NewTimer(h.maxDuration)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			// The client is gone. The install keeps running; only this view
			// of it ends.
			return
		case p, ok := <-events:
			if !ok {
				// The subscription ended under us. The poll below is now the
				// only source of truth, so keep the connection until it
				// reaches a terminal state or the cap expires.
				events = nil
				continue
			}
			event := skillEventFromProgress(p)
			lastPercent = event.Percent
			if !h.emit(c, event) || event.Done {
				return
			}
		case <-poll.C:
			current, err := h.service.GetSkill(ctx, tenantID, configID, skillID)
			if err != nil {
				logger.Warnf(ctx, "[skill] re-read %s while streaming failed: %v", skillID, err)
				if !h.emitComment(c) {
					return
				}
				continue
			}
			if terminal, ok := terminalSkillEvent(current); ok {
				h.emit(c, terminal)
				return
			}
			// Nothing to report: a comment keeps the connection warm and
			// surfaces a client that has already gone away.
			if !h.emitComment(c) {
				return
			}
		case <-deadline.C:
			h.emit(c, skillInstallEvent{
				Percent: lastPercent,
				Stage:   skillStageDetached,
				Status:  skill.Status,
				Log:     "stopped following this run; reconnect or poll the skill for its status",
				Done:    true,
			})
			return
		}
	}
}

// InstallTranscript godoc
// @Summary      Follow an install's agent transcript
// @Description  Server-sent replay of everything the installer agent did — the
// @Description  prompt it was given, its thinking, the commands it ran and
// @Description  their output — followed live while the install is still
// @Description  running. Frames are the same shape the chat stream uses, so a
// @Description  console renders an install with the components it renders a
// @Description  chat turn with. 404 once the event log has expired; the
// @Description  durable message history is the fallback.
// @Tags         SandboxConfig
// @Produce      text/event-stream
// @Param        id       path      string  true  "Sandbox config ID"
// @Param        skillId  path      string  true  "Skill ID"
// @Success      200      {string}  string  "SSE stream of transcript events"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Skill or transcript not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/skills/{skillId}/transcript [get]
//
// The transcript deliberately does not reuse /sessions/continue-stream. That
// endpoint authorizes by "does this chat session belong to you", while an
// install is a workspace-level maintenance run whose session is hidden from the
// session list on purpose — the two rules pull in opposite directions. Here the
// skill lookup above is the authorization, exactly as it is for every other
// route in this file.
func (h *SandboxSkillHandler) InstallTranscript(c *gin.Context) {
	ctx := c.Request.Context()

	skill, err := h.resolveSkill(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	sessionID, messageID := skill.InstallSessionID, skill.InstallMessageID
	if sessionID == "" || messageID == "" {
		_ = c.Error(apperrors.NewNotFoundError("this skill has no install transcript"))
		return
	}
	if h.streams == nil {
		_ = c.Error(apperrors.NewNotFoundError("install transcripts are unavailable"))
		return
	}

	events, offset, err := h.streams.GetEvents(ctx, sessionID, messageID, 0)
	if err != nil {
		logger.Errorf(ctx, "[skill] read install transcript of %s failed: %v", skill.ID, err)
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	// An empty log means the run predates the transcript or its TTL has passed.
	// Refuse before any SSE header is written so the caller can still fall back
	// to the durable message history.
	if len(events) == 0 {
		_ = c.Error(apperrors.NewNotFoundError("this install's event log is no longer available"))
		return
	}

	setSandboxSkillSSEHeaders(c)

	done := h.emitTranscript(c, sessionID, messageID, events)
	if done {
		return
	}

	// Tailing at the chat stream's cadence: the installer emits thinking token
	// by token, and a slower tick would arrive as visible bursts.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(h.maxDuration)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			// The viewer navigated away. The install keeps running.
			return
		case <-deadline.C:
			return
		case <-tick.C:
			newEvents, newOffset, err := h.streams.GetEvents(ctx, sessionID, messageID, offset)
			if err != nil {
				logger.Warnf(ctx, "[skill] tail install transcript of %s failed: %v", skill.ID, err)
				return
			}
			offset = newOffset
			if h.emitTranscript(c, sessionID, messageID, newEvents) {
				return
			}
		}
	}
}

// emitTranscript writes frames and reports whether the stream is over, either
// because the run completed or because the viewer is gone.
func (h *SandboxSkillHandler) emitTranscript(
	c *gin.Context, sessionID, messageID string, events []interfaces.StreamEvent,
) bool {
	for _, evt := range events {
		c.SSEvent("message", types.StreamResponse{
			// Every frame carries the assistant message ID so the console
			// groups the whole run into one turn.
			ID:                 messageID,
			ResponseType:       evt.Type,
			Content:            evt.Content,
			Done:               evt.Done,
			SessionID:          sessionID,
			AssistantMessageID: messageID,
			Data:               evt.Data,
		})
		c.Writer.Flush()
		if c.Request.Context().Err() != nil {
			return true
		}
		if evt.Type == types.ResponseTypeComplete {
			return true
		}
	}
	return c.Request.Context().Err() != nil
}

// resolveSkill loads the skill for the caller's workspace and config, and
// returns the 404 every route renders when it is not reachable.
func (h *SandboxSkillHandler) resolveSkill(c *gin.Context) (*types.TenantSkillEntity, error) {
	skill, err := h.service.GetSkill(c.Request.Context(), sandboxConfigTenantID(c),
		c.Param("id"), c.Param("skillId"))
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	return skill, nil
}

// emit writes one frame and reports whether the stream can continue.
func (h *SandboxSkillHandler) emit(c *gin.Context, event skillInstallEvent) bool {
	c.SSEvent("message", event)
	c.Writer.Flush()
	return c.Request.Context().Err() == nil
}

func (h *SandboxSkillHandler) emitComment(c *gin.Context) bool {
	if _, err := c.Writer.WriteString(": keep-alive\n\n"); err != nil {
		return false
	}
	c.Writer.Flush()
	return c.Request.Context().Err() == nil
}

// setSandboxSkillSSEHeaders mirrors the session package's SSE preamble;
// X-Accel-Buffering is what stops nginx from holding progress frames back.
func setSandboxSkillSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}
