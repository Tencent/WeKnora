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
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
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

	installID  string
	installErr error
	removeErr  error

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
	removeTenant  uint64
	removeConfig  string
	removeSkill   string
	patchEnabled  bool
	closed        bool
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

func (f *fakeSandboxSkillService) SetSkillEnabled(
	_ context.Context, tenantID uint64, configID, skillID string, enabled bool,
) (*types.TenantSkillEntity, error) {
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	f.patchEnabled = enabled
	skill := f.skills[skillID]
	if skill == nil || tenantID != testSkillTenantID || skill.SandboxConfigID != configID {
		return nil, nil
	}
	skill.Enabled = enabled
	return skill, nil
}

func (f *fakeSandboxSkillService) InstallSkill(
	_ context.Context, tenantID uint64, configID string, archive []byte,
) (string, error) {
	f.installTenant, f.installConfig, f.installBytes = tenantID, configID, archive
	return f.installID, f.installErr
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
	r.PATCH("/sandbox-configs/:id/skills/:skillId", h.Patch)
	r.DELETE("/sandbox-configs/:id/skills/:skillId", h.Delete)
	r.GET("/sandbox-configs/:id/skills/:skillId/install-events", h.InstallEvents)
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
	svc := &fakeSandboxSkillService{installID: "skill-1"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, skillUploadRequest(t, "cfg-a", []byte("not a zip")))

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "SKILL.md is missing")
}

// The install runs for minutes, so the upload is only ever accepted; the ID is
// what the client needs to follow it.
func TestSandboxSkillUploadAcceptedReturnsSkillID(t *testing.T) {
	svc := &fakeSandboxSkillService{installID: "skill-7"}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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

func TestSandboxSkillUploadWithoutFileReturns400(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

	for _, tc := range []struct {
		name    string
		method  string
		target  string
		body    string
		wantGot int
	}{
		{"get own skill", http.MethodGet, "/sandbox-configs/cfg-a/skills/skill-1", "", http.StatusOK},
		{"get skill of another config", http.MethodGet, "/sandbox-configs/cfg-b/skills/skill-1", "", http.StatusNotFound},
		{"patch skill of another config", http.MethodPatch, "/sandbox-configs/cfg-b/skills/skill-1", `{"enabled":false}`, http.StatusNotFound},
		{"stream skill of another config", http.MethodGet, "/sandbox-configs/cfg-b/skills/skill-1/install-events", "", http.StatusNotFound},
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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/sandbox-configs/cfg-a/skills/skill-1",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.True(t, svc.skills["skill-1"].Enabled)
}

// Removal rebuilds the image, so it is accepted and followed, never awaited.
func TestSandboxSkillDeleteIsAccepted(t *testing.T) {
	svc := &fakeSandboxSkillService{}
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/sandbox-configs/cfg-a/skills/skill-1", nil))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, testSkillTenantID, svc.removeTenant)
	require.Equal(t, "cfg-a", svc.removeConfig)
	require.Equal(t, "skill-1", svc.removeSkill)
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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	h := NewSandboxSkillHandler(svc)
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
	router := newSkillTestRouter(NewSandboxSkillHandler(svc))

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
	h := NewSandboxSkillHandler(svc)
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
	h := NewSandboxSkillHandler(svc)
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
