package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const testSkillTenantID = uint64(42)

// fakeSandboxSkillService mirrors the real service surface: every method takes
// the workspace ID the route resolved, and the progress subscription honours
// the request context and hands back a closer the handler must call.
type fakeSandboxSkillService struct {
	mu sync.Mutex

	skills   map[string]*types.TenantSkillEntity
	listErr  error
	getErr   error
	patchErr error

	installID     string
	installErr    error
	installSource string
	sourceErr     error
	reinstallErr  error
	removeErr     error

	files    []service.SkillFileEntry
	file     *service.SkillFileContent
	filesErr error
	fileErr  error
	filePath string

	last      service.SkillProgress
	hasLast   bool
	events    chan service.SkillProgress
	subscribe error

	// Recorded calls. Every one keeps the workspace ID so a route that
	// forgot to scope its lookups fails the test.
	listTenant    uint64
	listConfig    string
	getCalls      int
	installTenant uint64
	installConfig string
	installBytes  []byte

	reinstallTenant uint64
	reinstallConfig string
	reinstallSkill  string

	removeTenant uint64
	removeConfig string
	removeSkill  string
	patchEnabled bool
	// patchEnvs is nil until a request actually carried the field, so a test
	// can tell "did not mention envs" from "sent an empty object".
	patchEnvs map[string]string
	// patchCalls counts service calls per request, so a handler that split
	// one PATCH back into two read-modify-write cycles fails the test.
	patchCalls int
	closed     bool
	// onGet runs on every read, so a test can model a row that disappears
	// while the client is streaming.
	onGet func(calls int)
}

func (f *fakeSandboxSkillService) ListSkills(
	_ context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillEntity, error) {
	f.listTenant, f.listConfig = tenantID, configID
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*types.TenantSkillEntity, 0, len(f.skills))
	for _, skill := range f.skills {
		out = append(out, skill)
	}
	return out, nil
}

func (f *fakeSandboxSkillService) GetSkill(
	_ context.Context, tenantID uint64, configID, skillID string,
) (*types.TenantSkillEntity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.onGet != nil {
		f.onGet(f.getCalls)
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	if tenantID != testSkillTenantID {
		return nil, nil
	}
	skill := f.skills[skillID]
	if skill == nil || skill.SandboxConfigID != configID {
		return nil, nil
	}
	return skill, nil
}

// UpdateSkillAdmin mirrors the real service: only declared names are written,
// and an unreachable skill is reported as nil rather than as an error. A fake
// that accepted any name would let the handler stop being the layer that keeps
// the declaration meaningful.
func (f *fakeSandboxSkillService) UpdateSkillAdmin(
	_ context.Context, tenantID uint64, configID, skillID string,
	update service.SkillAdminUpdate,
) (*types.TenantSkillEntity, error) {
	f.patchCalls++
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	if update.Enabled != nil {
		f.patchEnabled = *update.Enabled
	}
	if update.EnvValues != nil {
		f.patchEnvs = update.EnvValues
	}
	skill := f.skills[skillID]
	if skill == nil || tenantID != testSkillTenantID || skill.SandboxConfigID != configID {
		return nil, nil
	}
	if update.Enabled != nil {
		skill.Enabled = *update.Enabled
	}
	for i := range skill.Envs {
		if value, sent := update.EnvValues[skill.Envs[i].Name]; sent {
			skill.Envs[i].Value = value
		}
	}
	return skill, nil
}

func (f *fakeSandboxSkillService) ListSkillFiles(
	_ context.Context, tenantID uint64, configID, skillID string,
) ([]service.SkillFileEntry, error) {
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	skill, err := f.GetSkill(context.Background(), tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	return f.files, nil
}

func (f *fakeSandboxSkillService) ReadSkillFile(
	_ context.Context, tenantID uint64, configID, skillID, relativePath string,
) (*service.SkillFileContent, error) {
	f.filePath = relativePath
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	skill, err := f.GetSkill(context.Background(), tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	return f.file, nil
}

func (f *fakeSandboxSkillService) InstallSkill(
	_ context.Context, tenantID uint64, configID string, archive []byte,
) (string, error) {
	f.installTenant, f.installConfig, f.installBytes = tenantID, configID, archive
	return f.installID, f.installErr
}

func (f *fakeSandboxSkillService) InstallSkillFromSource(
	_ context.Context, tenantID uint64, configID, source string,
) (string, error) {
	f.installTenant, f.installConfig = tenantID, configID
	f.installSource = source
	return f.installID, f.sourceErr
}

func (f *fakeSandboxSkillService) ReinstallSkill(
	_ context.Context, tenantID uint64, configID, skillID string,
) (string, error) {
	f.reinstallTenant, f.reinstallConfig, f.reinstallSkill = tenantID, configID, skillID
	return f.installID, f.reinstallErr
}

func (f *fakeSandboxSkillService) RemoveSkill(
	_ context.Context, tenantID uint64, configID, skillID string,
) error {
	f.removeTenant, f.removeConfig, f.removeSkill = tenantID, configID, skillID
	return f.removeErr
}

func (f *fakeSandboxSkillService) LastProgress(
	_ context.Context, _ uint64, _, _ string,
) (service.SkillProgress, bool) {
	return f.last, f.hasLast
}

func (f *fakeSandboxSkillService) SubscribeProgress(
	ctx context.Context, _ uint64, _, _ string,
) (<-chan service.SkillProgress, func(), error) {
	if f.subscribe != nil {
		return nil, func() {}, f.subscribe
	}
	closer := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.closed = true
	}
	if f.events == nil {
		return nil, closer, nil
	}
	// The real subscription stops delivering once the request context is
	// gone; a fake that kept delivering would hide a handler that ignores it.
	out := make(chan service.SkillProgress, cap(f.events))
	go func() {
		defer close(out)
		for {
			select {
			case p, ok := <-f.events:
				if !ok {
					return
				}
				select {
				case out <- p:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, closer, nil
}

func (f *fakeSandboxSkillService) subscriptionClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func newSkillTestRouter(h *SandboxSkillHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), testSkillTenantID)
		c.Next()
	})
	r.GET("/sandbox-configs/:id/skills", h.List)
	r.POST("/sandbox-configs/:id/skills", h.Upload)
	r.GET("/sandbox-configs/:id/skills/:skillId", h.Get)
	r.GET("/sandbox-configs/:id/skills/:skillId/files", h.ListFiles)
	r.GET("/sandbox-configs/:id/skills/:skillId/files/content", h.GetFile)
	r.POST("/sandbox-configs/:id/skills/:skillId/reinstall", h.Reinstall)
	r.PATCH("/sandbox-configs/:id/skills/:skillId", h.Patch)
	r.DELETE("/sandbox-configs/:id/skills/:skillId", h.Delete)
	r.GET("/sandbox-configs/:id/skills/:skillId/install-events", h.InstallEvents)
	r.GET("/sandbox-configs/:id/skills/:skillId/transcript", h.InstallTranscript)
	return r
}

func skillUploadRequest(t *testing.T, configID string, archive []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "skill.zip")
	require.NoError(t, err)
	_, err = part.Write(archive)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/"+configID+"/skills", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// An upload larger than the platform's file cap must be refused by the HTTP
// layer, before the whole body is buffered for the parser.
func TestSandboxSkillUploadOverLimitReturns400(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	t.Setenv("MAX_SKILL_BUNDLE_SIZE_MB", "1")
	svc := &fakeSandboxSkillService{installID: "skill-1"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, skillUploadRequest(t, "cfg-a", bytes.Repeat([]byte("z"), 2<<20)))

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "1 MB")
	require.Nil(t, svc.installBytes, "an oversize body must never reach the service")
}

// Every rejection of an uploaded archive is one class of error, matched as a
// sentinel so a reworded message cannot start returning 500 for bad input.
func TestSandboxSkillUploadInvalidBundleReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{
		installErr: fmt.Errorf("%w: SKILL.md is missing", service.ErrSkillBundleInvalid),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, skillUploadRequest(t, "cfg-a", []byte("not a zip")))

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "SKILL.md is missing")
}

// The install runs for minutes, so the upload is only ever accepted; the ID is
// what the client needs to follow it.
func TestSandboxSkillUploadAcceptedReturnsSkillID(t *testing.T) {
	svc := &fakeSandboxSkillService{installID: "skill-7"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, skillUploadRequest(t, "cfg-a", []byte("zip-bytes")))

	require.Equal(t, http.StatusAccepted, w.Code)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			SkillID string `json:"skill_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "skill-7", payload.Data.SkillID)

	require.Equal(t, testSkillTenantID, svc.installTenant)
	require.Equal(t, "cfg-a", svc.installConfig)
	require.Equal(t, []byte("zip-bytes"), svc.installBytes)
}

func TestSandboxSkillInstallFromSourceAcceptedReturnsSkillID(t *testing.T) {
	svc := &fakeSandboxSkillService{installID: "skill-9"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	body := `{"source":"@owner/demo"}`
	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, testSkillTenantID, svc.installTenant)
	require.Equal(t, "cfg-a", svc.installConfig)
	require.Equal(t, "@owner/demo", svc.installSource)
	require.Nil(t, svc.installBytes, "a source install must not look like an upload")
}

func TestSandboxSkillInstallFromSourceRequiresSource(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(`{"source":"  "}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Empty(t, svc.installSource)
}

func TestSandboxSkillInstallFromSourceInvalidReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{
		sourceErr: fmt.Errorf("%w: only http(s) sources are allowed", service.ErrSkillSourceInvalid),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(`{"source":"file:///etc/passwd"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "only http(s) sources are allowed")
}

func TestSandboxSkillInstallFromSourceAmbiguousShorthandReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{
		sourceErr: fmt.Errorf("%w: %q is ambiguous; use @owner/slug for ClawHub",
			service.ErrSkillSourceInvalid, "owner/slug"),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(`{"source":"owner/slug"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "ambiguous")
	require.Equal(t, "owner/slug", svc.installSource)
}

func TestSandboxSkillInstallFromSourceRejectsAnOversizedJSONBody(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(string(oversizedSkillSourceJSON(1))))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "skill source request is too large")
	require.Empty(t, svc.installSource)
}

func TestSandboxSkillUploadWithoutFileReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sandbox-configs/cfg-a/skills",
		strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=none")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Nil(t, svc.installBytes)
}

// Every route must scope to the caller's workspace, and a skill under another
// workspace's config must be unreachable rather than merely empty.
func TestSandboxSkillRoutesScopeToCallerWorkspace(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {
			ID: "skill-1", TenantID: testSkillTenantID, SandboxConfigID: "cfg-a",
			Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	for _, tc := range []struct {
		name    string
		method  string
		target  string
		body    string
		wantGot int
	}{
		{
			name: "get own skill", method: http.MethodGet,
			target: "/sandbox-configs/cfg-a/skills/skill-1", wantGot: http.StatusOK,
		},
		{
			name: "get skill of another config", method: http.MethodGet,
			target: "/sandbox-configs/cfg-b/skills/skill-1", wantGot: http.StatusNotFound,
		},
		{
			name: "patch skill of another config", method: http.MethodPatch,
			target: "/sandbox-configs/cfg-b/skills/skill-1", body: `{"enabled":false}`,
			wantGot: http.StatusNotFound,
		},
		{
			name: "stream skill of another config", method: http.MethodGet,
			target:  "/sandbox-configs/cfg-b/skills/skill-1/install-events",
			wantGot: http.StatusNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, tc.wantGot, w.Code)
		})
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sandbox-configs/cfg-a/skills", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, testSkillTenantID, svc.listTenant)
	require.Equal(t, "cfg-a", svc.listConfig)
}

func TestSandboxSkillListReturnsProjection(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {
			ID: "skill-1", TenantID: testSkillTenantID, SandboxConfigID: "cfg-a",
			Name: "pdf", Version: "1.2.0", Description: "read pdfs",
			Status: types.SkillStatusReady, Enabled: true, BundleSHA256: "abc",
			InstalledSnapshotID: "snap-1",
		},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sandbox-configs/cfg-a/skills", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var payload struct {
		Data []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Version  string `json:"version"`
			Status   string `json:"status"`
			Enabled  bool   `json:"enabled"`
			Snapshot string `json:"installed_snapshot_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 1)
	require.Equal(t, "skill-1", payload.Data[0].ID)
	require.Equal(t, "1.2.0", payload.Data[0].Version)
	require.Equal(t, types.SkillStatusReady, payload.Data[0].Status)
	require.True(t, payload.Data[0].Enabled)
	require.Equal(t, "snap-1", payload.Data[0].Snapshot)
}

func TestSandboxSkillPatchTogglesEnabled(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {
			ID: "skill-1", TenantID: testSkillTenantID, SandboxConfigID: "cfg-a",
			Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, svc.patchEnabled)
	require.Contains(t, w.Body.String(), `"enabled":false`)
}

// An empty body must not be read as "disable it": the field is the whole
// request, so its absence is a bad request rather than a silent false.
func TestSandboxSkillPatchWithoutEnabledFieldReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Enabled: true},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.True(t, svc.skills["skill-1"].Enabled)
}

func TestSandboxSkillPatchRejectsAnOversizedJSONBody(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {
			ID: "skill-1", TenantID: testSkillTenantID, SandboxConfigID: "cfg-a",
			Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		bytes.NewReader(oversizedSkillSourceJSON(1)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "skill request is too large")
	require.Equal(t, 0, svc.patchCalls)
}

// Removal rebuilds the image, so it is accepted and followed, never awaited.
func TestSandboxSkillDeleteIsAccepted(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/sandbox-configs/cfg-a/skills/skill-1", nil))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, testSkillTenantID, svc.removeTenant)
	require.Equal(t, "cfg-a", svc.removeConfig)
	require.Equal(t, "skill-1", svc.removeSkill)
}

// A retry boots a sandbox and runs for minutes, exactly like the upload it
// stands in for, so it is accepted and followed rather than awaited.
func TestSandboxSkillReinstallIsAccepted(t *testing.T) {
	svc := &fakeSandboxSkillService{installID: "skill-1"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
		"/sandbox-configs/cfg-a/skills/skill-1/reinstall", nil))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, testSkillTenantID, svc.reinstallTenant,
		"a retry must be scoped to the caller's workspace")
	require.Equal(t, "cfg-a", svc.reinstallConfig)
	require.Equal(t, "skill-1", svc.reinstallSkill)
	require.Contains(t, w.Body.String(), `"skill_id":"skill-1"`)
}

func TestSandboxSkillReinstallReportsAMissingSkill(t *testing.T) {
	svc := &fakeSandboxSkillService{
		reinstallErr: apperrors.NewNotFoundError("skill not found"),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
		"/sandbox-configs/cfg-a/skills/nope/reinstall", nil))

	require.Equal(t, http.StatusNotFound, w.Code)
}

func decodeSSEEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var payload map[string]any
		require.NoError(t, json.Unmarshal(
			[]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload))
		events = append(events, payload)
	}
	return events
}

// A client that connects after the install finished must be told so and get its
// connection closed, not left waiting for an event that already happened.
func TestSandboxSkillInstallEventsFinishedInstallTerminatesImmediately(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {
				ID: "skill-1", SandboxConfigID: "cfg-a",
				Status: types.SkillStatusReady, Enabled: true,
			},
		},
		events: make(chan service.SkillProgress),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")

	events := decodeSSEEvents(t, w.Body.String())
	require.NotEmpty(t, events)
	final := events[len(events)-1]
	require.Equal(t, true, final["done"])
	require.Equal(t, types.SkillStatusReady, final["status"])
	require.True(t, svc.subscriptionClosed(), "the subscription must be released")
}

// A late subscriber still gets the last stored value, so the UI can paint
// without waiting for the next tick.
func TestSandboxSkillInstallEventsReplaysLastProgress(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
		},
		last:    service.SkillProgress{Percent: 35, Stage: "seeded"},
		hasLast: true,
		events:  make(chan service.SkillProgress, 1),
	}
	svc.events <- service.SkillProgress{
		Percent: 100, Stage: "done", Status: types.SkillStatusReady,
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	events := decodeSSEEvents(t, w.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "seeded", events[0]["stage"])
	require.Equal(t, false, events[0]["done"])
	require.Equal(t, "done", events[1]["stage"])
	require.Equal(t, true, events[1]["done"])
}

// A failed run is terminal too: the client must not keep a connection open
// waiting for a success that will never come.
func TestSandboxSkillInstallEventsFailureTerminatesStream(t *testing.T) {
	events := make(chan service.SkillProgress, 1)
	events <- service.SkillProgress{
		Percent: 100, Stage: "failed", Status: types.SkillStatusFailed, Log: "smoke run failed",
	}
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
		},
		events: events,
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	decoded := decodeSSEEvents(t, w.Body.String())
	require.NotEmpty(t, decoded)
	final := decoded[len(decoded)-1]
	require.Equal(t, true, final["done"])
	require.Equal(t, "smoke run failed", final["log"])
}

// A duplicate removal submission finishes without publishing anything: the
// second run finds nothing to remove and returns. The durable state is the only
// thing left that says so, so the stream must watch it and end itself.
func TestSandboxSkillInstallEventsSynthesizesTerminalWhenRowDisappears(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusRemoving},
		},
		last: service.SkillProgress{
			Percent: 5, Stage: "accepted", Status: types.SkillStatusRemoving,
		},
		hasLast: true,
		events:  make(chan service.SkillProgress),
	}
	// The row is gone by the first re-check, exactly as it is once the first
	// removal has finished.
	svc.onGet = func(calls int) {
		if calls > 1 {
			svc.skills = map[string]*types.TenantSkillEntity{}
		}
	}
	h := NewSandboxSkillHandler(svc, nil)
	h.pollInterval = 10 * time.Millisecond
	router := newSkillTestRouter(h)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	events := decodeSSEEvents(t, w.Body.String())
	require.NotEmpty(t, events)
	final := events[len(events)-1]
	require.Equal(t, true, final["done"])
	require.Equal(t, "removed", final["status"])
}

// Without Redis there is no live progress at all. One event describing the
// durable state is honest; holding the connection open is not.
func TestSandboxSkillInstallEventsWithoutRedisSendsStateAndCloses(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
		},
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	events := decodeSSEEvents(t, w.Body.String())
	require.Len(t, events, 1)
	require.Equal(t, true, events[0]["done"])
	require.Equal(t, types.SkillStatusInstalling, events[0]["status"])
	require.True(t, svc.subscriptionClosed())
}

// A disconnected client must free the subscription rather than leaving a
// goroutine and a Redis connection behind for the rest of the install.
func TestSandboxSkillInstallEventsStopsWhenClientDisconnects(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
		},
		events: make(chan service.SkillProgress),
	}
	h := NewSandboxSkillHandler(svc, nil)
	h.pollInterval = time.Hour
	router := newSkillTestRouter(h)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept streaming after the client went away")
	}
	require.True(t, svc.subscriptionClosed())
}

// A run whose process died leaves the row at "installing" forever until the
// reaper lands. The stream must still end by itself.
func TestSandboxSkillInstallEventsStopsFollowingAfterCap(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
		},
		events: make(chan service.SkillProgress),
	}
	h := NewSandboxSkillHandler(svc, nil)
	h.pollInterval = time.Hour
	h.maxDuration = 20 * time.Millisecond
	router := newSkillTestRouter(h)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/cfg-a/skills/skill-1/install-events", nil))

	events := decodeSSEEvents(t, w.Body.String())
	require.NotEmpty(t, events)
	final := events[len(events)-1]
	require.Equal(t, true, final["done"])
	require.Equal(t, types.SkillStatusInstalling, final["status"],
		"giving up on following must not be reported as a finished install")
}

// The transcript locators are the only way the console can find an install's
// conversation, so they must survive the outward projection.
func TestToSkillResponseCarriesTranscriptLocators(t *testing.T) {
	got := toSkillResponse(&types.TenantSkillEntity{
		ID:               "sk-1",
		Name:             "pdf-tools",
		Status:           types.SkillStatusReady,
		InstallSessionID: "sess-1",
		InstallMessageID: "msg-1",
	})
	require.Equal(t, "sess-1", got.InstallSessionID)
	require.Equal(t, "msg-1", got.InstallMessageID)
}

// A skill installed before this feature shipped has no locators; the fields
// must be absent rather than empty so the console can hide the entry point.
func TestToSkillResponseOmitsMissingTranscriptLocators(t *testing.T) {
	raw, err := json.Marshal(toSkillResponse(&types.TenantSkillEntity{ID: "sk-1"}))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "install_session_id")
	require.NotContains(t, string(raw), "install_message_id")
}

// transcriptStreamManager is the installer's event log. Appends land in the
// same slice reads serve, so a test can grow the log while the handler tails it.
type transcriptStreamManager struct {
	mu     sync.Mutex
	key    string
	events []interfaces.StreamEvent
	err    error
}

func (m *transcriptStreamManager) AppendEvent(
	_ context.Context, sessionID, messageID string, evt interfaces.StreamEvent,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.key = sessionID + "/" + messageID
	m.events = append(m.events, evt)
	return nil
}

func (m *transcriptStreamManager) GetEvents(
	_ context.Context, sessionID, messageID string, from int,
) ([]interfaces.StreamEvent, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, 0, m.err
	}
	// Recording the key on read as well is what proves the handler addressed
	// the log by the skill's stored locators rather than by anything else.
	m.key = sessionID + "/" + messageID
	if from >= len(m.events) {
		return nil, len(m.events), nil
	}
	out := append([]interfaces.StreamEvent(nil), m.events[from:]...)
	return out, len(m.events), nil
}

func (m *transcriptStreamManager) readKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.key
}

func transcriptSkillService() *fakeSandboxSkillService {
	return &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {
			ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling,
			InstallSessionID: "sess-9", InstallMessageID: "msg-9",
		},
	}}
}

func transcriptRequest(configID, skillID string) *http.Request {
	return httptest.NewRequest(http.MethodGet,
		"/sandbox-configs/"+configID+"/skills/"+skillID+"/transcript", nil)
}

// The whole point of the endpoint: everything the installer did, in order,
// shaped like the chat stream so the console renders it with the components it
// already has.
func TestSandboxSkillTranscriptReplaysTheInstallerConversation(t *testing.T) {
	streams := &transcriptStreamManager{}
	ctx := context.Background()
	for _, evt := range []interfaces.StreamEvent{
		{ID: "p", Type: types.ResponseTypeInstallPrompt, Content: "install web-search", Done: true},
		{ID: "t", Type: types.ResponseTypeThinking, Content: "check for uv"},
		{
			ID: "c", Type: types.ResponseTypeToolCall, Content: "Calling tool: shell_exec",
			Data: map[string]interface{}{"tool_name": "shell_exec"},
		},
		{
			ID: "r", Type: types.ResponseTypeToolResult, Content: "uv 0.4.0",
			Data: map[string]interface{}{"success": true},
		},
		{ID: "done", Type: types.ResponseTypeComplete, Done: true},
	} {
		require.NoError(t, streams.AppendEvent(ctx, "sess-9", "msg-9", evt))
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(transcriptSkillService(), streams))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-a", "skill-1"))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.Equal(t, "sess-9/msg-9", streams.readKey())

	frames := parseTranscriptFrames(t, w.Body.String())
	require.Equal(t, []string{"install_prompt", "thinking", "tool_call", "tool_result", "complete"},
		transcriptTypes(frames))
	require.Equal(t, "install web-search", frames[0].Content)
	// Every frame names the same turn, or the console would scatter one install
	// across five messages.
	for _, frame := range frames {
		require.Equal(t, "msg-9", frame.ID)
		require.Equal(t, "msg-9", frame.AssistantMessageID)
		require.Equal(t, "sess-9", frame.SessionID)
	}
	require.Equal(t, "shell_exec", frames[2].Data["tool_name"])
}

// A running install is the case the user actually watches: the handler must
// keep the connection open and push what the agent does next.
func TestSandboxSkillTranscriptTailsARunningInstall(t *testing.T) {
	streams := &transcriptStreamManager{}
	ctx := context.Background()
	require.NoError(t, streams.AppendEvent(ctx, "sess-9", "msg-9", interfaces.StreamEvent{
		ID: "p", Type: types.ResponseTypeInstallPrompt, Content: "install web-search", Done: true,
	}))
	router := newSkillTestRouter(NewSandboxSkillHandler(transcriptSkillService(), streams))

	// The engine keeps working after the console attaches.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = streams.AppendEvent(ctx, "sess-9", "msg-9", interfaces.StreamEvent{
			ID: "t", Type: types.ResponseTypeThinking, Content: "creating the venv",
		})
		_ = streams.AppendEvent(ctx, "sess-9", "msg-9", interfaces.StreamEvent{
			ID: "done", Type: types.ResponseTypeComplete, Done: true,
		})
	}()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-a", "skill-1"))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"install_prompt", "thinking", "complete"},
		transcriptTypes(parseTranscriptFrames(t, w.Body.String())))
}

// Refusing before the SSE headers go out is what lets the console fall back to
// the durable message history instead of rendering an empty conversation.
func TestSandboxSkillTranscriptWithExpiredLogReturns404(t *testing.T) {
	router := newSkillTestRouter(
		NewSandboxSkillHandler(transcriptSkillService(), &transcriptStreamManager{}),
	)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-a", "skill-1"))

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	require.NotContains(t, w.Header().Get("Content-Type"), "event-stream")
}

func TestSandboxSkillTranscriptWithoutLocatorsReturns404(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusReady},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, &transcriptStreamManager{}))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-a", "skill-1"))

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

func TestSandboxSkillTranscriptWhileInstallIsPreparingReturns204(t *testing.T) {
	svc := &fakeSandboxSkillService{skills: map[string]*types.TenantSkillEntity{
		"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Status: types.SkillStatusInstalling},
	}}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, &transcriptStreamManager{}))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-a", "skill-1"))

	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
	require.NotContains(t, w.Header().Get("Content-Type"), "event-stream")
}

// The skill lookup is this endpoint's authorization: an install transcript can
// hold command output from another workspace's image build.
func TestSandboxSkillTranscriptOfAnotherConfigReturns404(t *testing.T) {
	streams := &transcriptStreamManager{}
	require.NoError(t, streams.AppendEvent(context.Background(), "sess-9", "msg-9",
		interfaces.StreamEvent{ID: "p", Type: types.ResponseTypeInstallPrompt, Content: "secret"}))
	router := newSkillTestRouter(NewSandboxSkillHandler(transcriptSkillService(), streams))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, transcriptRequest("cfg-b", "skill-1"))

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	require.NotContains(t, w.Body.String(), "secret")
}

func parseTranscriptFrames(t *testing.T, body string) []types.StreamResponse {
	t.Helper()
	var frames []types.StreamResponse
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var frame types.StreamResponse
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(payload)), &frame), "line=%q", line)
		frames = append(frames, frame)
	}
	return frames
}

func transcriptTypes(frames []types.StreamResponse) []string {
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		out = append(out, string(frame.ResponseType))
	}
	return out
}

func TestSandboxSkillListFilesReturnsTheArchive(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Name: "pdf"},
		},
		files: []service.SkillFileEntry{
			{Path: "SKILL.md", Size: 12},
			{Path: "scripts/run.py", Size: 20},
		},
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sandbox-configs/cfg-a/skills/skill-1/files", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var body struct {
		Success bool                     `json:"success"`
		Data    []service.SkillFileEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, svc.files, body.Data)
}

func TestSandboxSkillListFilesMissingSkillReturns404(t *testing.T) {
	router := newSkillTestRouter(NewSandboxSkillHandler(&fakeSandboxSkillService{}, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sandbox-configs/cfg-a/skills/missing/files", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

func TestSandboxSkillGetFileReturnsContent(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a", Name: "pdf"},
		},
		file: &service.SkillFileContent{
			Path:     "scripts/run.py",
			Size:     11,
			Encoding: "utf-8",
			Content:  "print('hi')",
		},
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet, "/sandbox-configs/cfg-a/skills/skill-1/files/content?path=scripts/run.py", nil,
	)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.Equal(t, "scripts/run.py", svc.filePath)
	var body struct {
		Success bool                     `json:"success"`
		Data    service.SkillFileContent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "print('hi')", body.Data.Content)
}

func TestSandboxSkillGetFileInvalidPathReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{
		skills: map[string]*types.TenantSkillEntity{
			"skill-1": {ID: "skill-1", SandboxConfigID: "cfg-a"},
		},
		fileErr: apperrors.NewBadRequestError("invalid skill file path: ../secret"),
	}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet, "/sandbox-configs/cfg-a/skills/skill-1/files/content?path=../secret", nil,
	)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	require.Equal(t, "../secret", svc.filePath)
}
