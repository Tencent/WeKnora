package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler"
	sessionhandler "github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type deleteEmbedChannelService struct {
	interfaces.EmbedChannelService
	channel *types.EmbedChannel
}

func (s deleteEmbedChannelService) LookupForEmbed(
	_ context.Context, id, token string,
) (*types.EmbedChannel, error) {
	if id != s.channel.ID || token != s.channel.PublishToken {
		return nil, service.ErrEmbedTokenInvalid
	}
	if !s.channel.Enabled {
		return nil, service.ErrEmbedChannelDisabled
	}
	return s.channel, nil
}

type deleteEmbedTenantService struct{ interfaces.TenantService }

func (deleteEmbedTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type deleteEmbedSessionService struct {
	interfaces.SessionService
	session *types.Session
	err     error
	called  bool
	owner   string
	tenant  uint64
}

func (s *deleteEmbedSessionService) GetSessionByID(
	_ context.Context, tenant uint64, id string,
) (*types.Session, error) {
	if s.session == nil || s.session.ID != id || s.session.TenantID != tenant {
		return nil, apperrors.ErrSessionNotFound
	}
	return s.session, nil
}

func (s *deleteEmbedSessionService) DeleteSession(ctx context.Context, id string) error {
	s.called = true
	s.owner = types.SessionOwnerIDFromContext(ctx)
	s.tenant = types.MustTenantIDFromContext(ctx)
	if s.err != nil {
		return s.err
	}
	if s.session == nil || s.session.ID != id || s.session.UserID != s.owner || s.session.TenantID != s.tenant {
		return apperrors.ErrSessionNotFound
	}
	s.session = nil
	return nil
}

func TestEmbedDeleteSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_SIGNING_KEY", "test-embed-delete-signing-key")
	for _, tc := range []struct {
		name string
		want int
	}{
		{"valid", http.StatusOK},
		{"missing token", http.StatusUnauthorized},
		{"invalid token", http.StatusUnauthorized},
		{"disabled channel", http.StatusForbidden},
		{"disallowed origin", http.StatusForbidden},
		{"missing signature", http.StatusForbidden},
		{"another session signature", http.StatusForbidden},
		{"another channel", http.StatusForbidden},
		{"another tenant", http.StatusNotFound},
		{"another owner", http.StatusNotFound},
		{"missing session", http.StatusNotFound},
		{"delete failure", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := &types.EmbedChannel{
				ID: "delete-" + strings.ReplaceAll(tc.name, " ", "-"), TenantID: 42,
				PublishToken: "em_delete_test", Enabled: true,
				AllowedOrigins: []byte(`["https://host.example"]`),
			}
			const id = "11111111-2222-3333-4444-555555555555"
			owner := types.EmbedSessionPrincipal(ch.TenantID, ch.ID, id).StorageID()
			svc := &deleteEmbedSessionService{session: &types.Session{
				ID: id, TenantID: ch.TenantID, UserID: owner, Description: service.EmbedSessionDescription(ch.ID),
			}}
			token, sig, origin := ch.PublishToken, service.SignEmbedSessionHandle(ch, id), "https://host.example"
			switch tc.name {
			case "missing token":
				token = ""
			case "invalid token":
				token = "wrong"
			case "disabled channel":
				ch.Enabled = false
			case "disallowed origin":
				origin = "https://other.example"
			case "missing signature":
				sig = ""
			case "another session signature":
				sig = service.SignEmbedSessionHandle(ch, "another-session")
			case "another channel":
				svc.session.Description = service.EmbedSessionDescription("another-channel")
			case "another tenant":
				svc.session.TenantID++
			case "another owner":
				svc.session.UserID = "someone-else"
			case "missing session":
				svc.session = nil
			case "delete failure":
				svc.err = errors.New("delete failed")
			}
			sessions := sessionhandler.NewHandler(svc,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			)
			embed := handler.NewEmbedChannelHandler(nil, svc, sessions, nil, nil, nil, nil, nil)
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			RegisterEmbedPublicRoutes(r, embed, deleteEmbedChannelService{channel: ch},
				deleteEmbedTenantService{}, nil, nil, nil)
			url := "/api/v1/embed/" + ch.ID + "/sessions/" + id
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodDelete, url, nil)
				req.Header.Set("Authorization", "Embed "+token)
				req.Header.Set("X-Embed-Session", sig)
				req.Header.Set("Origin", origin)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				return w
			}
			w := request()
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.name == "valid" {
				require.JSONEq(t, `{"success":true,"message":"Session deleted successfully"}`, w.Body.String())
				require.Equal(t, owner, svc.owner)
				require.Equal(t, ch.TenantID, svc.tenant)
				require.Equal(t, http.StatusNotFound, request().Code)
			} else if tc.name != "another owner" && tc.name != "delete failure" {
				require.False(t, svc.called, "unauthorized requests must not reach deletion")
			}
		})
	}
}
