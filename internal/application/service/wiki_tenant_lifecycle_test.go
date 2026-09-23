package service

import (
	"context"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type wikiTenantLifecycleService struct {
	interfaces.TenantService
	err error
}

func (s *wikiTenantLifecycleService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &types.Tenant{ID: 7}, nil
}

func TestGenerateWithTemplateStopsBeforeLLMWhenTenantDeleted(t *testing.T) {
	model := &templateCaptureChatModel{response: "should not be returned"}
	svc := &wikiIngestService{
		tenantService: &wikiTenantLifecycleService{err: apprepo.ErrTenantNotFound},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, err := svc.generateWithTemplate(ctx, model, "hello {{.Content}}", map[string]string{"Content": "world"})

	require.ErrorIs(t, err, ErrWikiTenantInactive)
	require.Empty(t, model.messages, "deleted tenants must not issue another LLM request")
}

func TestGenerateWithTemplateKeepsActiveTenantBehavior(t *testing.T) {
	model := &templateCaptureChatModel{response: "ok"}
	svc := &wikiIngestService{
		tenantService: &wikiTenantLifecycleService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	got, err := svc.generateWithTemplate(ctx, model, "hello {{.Content}}", map[string]string{"Content": "world"})

	require.NoError(t, err)
	require.Equal(t, "ok", got)
	require.Len(t, model.messages, 1)
}
