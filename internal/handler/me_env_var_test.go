package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeMeEnvVarService struct {
	groups  []service.ConfigEnvGroup
	listErr error
	setErr  error
	delErr  error

	setScope, setName, setValue string
	delScope, delName           string
}

func (f *fakeMeEnvVarService) ListMine(context.Context) ([]service.ConfigEnvGroup, error) {
	return f.groups, f.listErr
}

func (f *fakeMeEnvVarService) SetMineSkill(_ context.Context, skillID, name, value string) error {
	f.setScope, f.setName, f.setValue = skillID, name, value
	return f.setErr
}

func (f *fakeMeEnvVarService) DeleteMineSkill(_ context.Context, skillID, name string) error {
	f.delScope, f.delName = skillID, name
	return f.delErr
}

func (f *fakeMeEnvVarService) SetMineSandbox(_ context.Context, configID, name, value string) error {
	f.setScope, f.setName, f.setValue = configID, name, value
	return f.setErr
}

func (f *fakeMeEnvVarService) DeleteMineSandbox(_ context.Context, configID, name string) error {
	f.delScope, f.delName = configID, name
	return f.delErr
}

func newMeEnvVarRouter(svc meEnvVarService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &MeEnvVarHandler{service: svc}
	r.GET("/me/env-vars", h.List)
	r.PUT("/me/env-vars/skill", h.SetSkill)
	r.DELETE("/me/env-vars/skill", h.DeleteSkill)
	r.PUT("/me/env-vars/sandbox", h.SetSandbox)
	r.DELETE("/me/env-vars/sandbox", h.DeleteSandbox)
	return r
}

func envVarRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// The principal is derived from the context, so the request type must have no
// field that could name one — absent rather than ignored, because an ignored
// field is one refactor away from being honoured.
func TestMeEnvVarRequestCannotCarryAPrincipal(t *testing.T) {
	rt := reflect.TypeOf(meEnvVarRequest{})
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		lower := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		for _, forbidden := range []string{"principal", "user"} {
			require.NotContains(t, lower, forbidden,
				"field %s would let a request choose whose values are touched", field.Name)
		}
	}

	raw, err := json.Marshal(meEnvVarRequest{})
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), "principal")
}

// A body that tries to name somebody else must simply not bind: the extra keys
// are dropped and the handler still passes only what the type carries.
func TestMeEnvVarSetIgnoresPrincipalFieldsInTheBody(t *testing.T) {
	svc := &fakeMeEnvVarService{}
	router := newMeEnvVarRouter(svc)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, envVarRequest(http.MethodPut, "/me/env-vars/skill",
		`{"skill_id":"sk-1","name":"API_TOKEN","value":"mine",`+
			`"principal_id":"bob","principal_type":"web_user","user_id":"bob"}`))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "sk-1", svc.setScope)
	require.Equal(t, "API_TOKEN", svc.setName)
	require.Equal(t, "mine", svc.setValue)
}

func TestEnvVarGroupJSONCarriesNoSecrets(t *testing.T) {
	updated := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	svc := &fakeMeEnvVarService{groups: []service.ConfigEnvGroup{{
		SandboxConfigID:   "cfg-a",
		SandboxConfigName: "Production",
		Vars: []service.EnvVarView{
			{Name: "HTTP_PROXY", Source: service.EnvSourceUser, UpdatedAt: &updated},
		},
		Skills: []service.SkillEnvGroup{{
			SkillID:   "sk-1",
			SkillName: "pdf-tools",
			Vars: []service.EnvVarView{
				{Name: "API_TOKEN", Description: "the workspace token", Required: true,
					Source: service.EnvSourceWorkspace},
				{Name: "USER_TOKEN", Source: service.EnvSourceUser, UpdatedAt: &updated},
				{Name: "REGION", Source: service.EnvSourceUnset},
			},
		}},
	}}}
	router := newMeEnvVarRouter(svc)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me/env-vars", nil))

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, forbidden := range []string{
		`"value"`, "instructions", "bundle_ref", "installed_snapshot_id",
		`"status"`, `"error"`,
	} {
		require.NotContains(t, body, forbidden)
	}
	require.Contains(t, body, `"sandbox_config_name":"Production"`)
	require.Contains(t, body, `"skill_name":"pdf-tools"`)
	require.Contains(t, body, `"source":"workspace"`)
	require.Contains(t, body, `"source":"user"`)
	require.Contains(t, body, `"source":"unset"`)
}

func TestMeEnvVarWritesRejectAnIncompleteBody(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/me/env-vars/skill", `{}`},
		{"/me/env-vars/skill", `{"skill_id":"sk-1"}`},
		{"/me/env-vars/skill", `{"name":"API_TOKEN"}`},
		// A skill id on the sandbox route names no config.
		{"/me/env-vars/sandbox", `{"skill_id":"sk-1","name":"API_TOKEN"}`},
		{"/me/env-vars/sandbox", `{"sandbox_config_id":"cfg-a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			svc := &fakeMeEnvVarService{}
			w := httptest.NewRecorder()
			newMeEnvVarRouter(svc).ServeHTTP(w, envVarRequest(http.MethodPut, tc.path, tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, svc.setScope)
		})
	}
}

// A refused name is the caller's mistake, and the service says so with an
// AppError; the handler must not turn that into a 500.
func TestMeEnvVarSetMapsAServiceRefusalTo400(t *testing.T) {
	svc := &fakeMeEnvVarService{
		setErr: apperrors.NewBadRequestError(`environment variable name "PATH" is reserved`),
	}
	w := httptest.NewRecorder()
	newMeEnvVarRouter(svc).ServeHTTP(w, envVarRequest(http.MethodPut, "/me/env-vars/sandbox",
		`{"sandbox_config_id":"cfg-a","name":"PATH","value":"x"}`))

	require.Equal(t, http.StatusBadRequest, w.Code)
}

// A repository failure is not the caller's mistake. Reporting it as 400 would
// both mislead and hand an internal message to any logged-in member.
func TestMeEnvVarSetDoesNotReportAServerFailureAs400(t *testing.T) {
	svc := &fakeMeEnvVarService{setErr: errors.New("dial tcp 10.0.0.5:5432: connect: refused")}
	w := httptest.NewRecorder()
	newMeEnvVarRouter(svc).ServeHTTP(w, envVarRequest(http.MethodPut, "/me/env-vars/sandbox",
		`{"sandbox_config_id":"cfg-a","name":"HTTP_PROXY","value":"x"}`))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "10.0.0.5")
}

func TestMeEnvVarDeleteMapsNothingToDeleteTo404(t *testing.T) {
	for _, path := range []string{"/me/env-vars/skill", "/me/env-vars/sandbox"} {
		t.Run(path, func(t *testing.T) {
			svc := &fakeMeEnvVarService{delErr: types.ErrEnvVarNotFound}
			body := `{"skill_id":"sk-1","sandbox_config_id":"cfg-a","name":"API_TOKEN"}`
			w := httptest.NewRecorder()
			newMeEnvVarRouter(svc).ServeHTTP(w, envVarRequest(http.MethodDelete, path, body))

			require.Equal(t, http.StatusNotFound, w.Code)
			require.Equal(t, "API_TOKEN", svc.delName)
		})
	}
}

func TestMeEnvVarDeleteRoutesToTheRequestedScope(t *testing.T) {
	body := `{"skill_id":"sk-1","sandbox_config_id":"cfg-a","name":"API_TOKEN"}`

	skillSvc := &fakeMeEnvVarService{}
	w := httptest.NewRecorder()
	newMeEnvVarRouter(skillSvc).ServeHTTP(w,
		envVarRequest(http.MethodDelete, "/me/env-vars/skill", body))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "sk-1", skillSvc.delScope)

	sandboxSvc := &fakeMeEnvVarService{}
	w = httptest.NewRecorder()
	newMeEnvVarRouter(sandboxSvc).ServeHTTP(w,
		envVarRequest(http.MethodDelete, "/me/env-vars/sandbox", body))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cfg-a", sandboxSvc.delScope)
}
