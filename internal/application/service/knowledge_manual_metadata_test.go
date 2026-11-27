package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestManualCustomMetadataValidation(t *testing.T) {
	t.Parallel()
	fields := map[string]interface{}{}
	for i := range 20 {
		fields[fmt.Sprintf("field-%d", i)] = true
	}
	maximum, err := json.Marshal(fields)
	require.NoError(t, err)
	fields["extra"] = true
	excess, err := json.Marshal(fields)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"flat values", `{"id":"9007199254740993","n":1.5,"flag":true,"parent":null}`, true},
		{"empty", `{}`, true},
		{"null", `null`, true},
		{"20 fields", string(maximum), true},
		{"21 fields", string(excess), false},
		{"64 byte key", `{"` + strings.Repeat("k", 64) + `":"value"}`, true},
		{"65 byte key", `{"` + strings.Repeat("k", 65) + `":"value"}`, false},
		{"multibyte key", `{"` + strings.Repeat("界", 22) + `":1}`, false},
		{"1000 byte value", `{"value":"` + strings.Repeat("界", 333) + `a"}`, true},
		{"1001 byte value", `{"value":"` + strings.Repeat("界", 333) + `aa"}`, false},
		{"blank key", `{" ":1}`, false},
		{"object value", `{"nested":{}}`, false},
		{"array value", `{"nested":[]}`, false},
		{"array root", `[]`, false},
		{"string root", `"value"`, false},
		{"number root", `12`, false},
		{"boolean root", `true`, false},
		{"number overflow", `{"n":1e400}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCustomMetadata([]byte(tc.raw))
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestManualCustomMetadataInvalidHasNoSideEffects(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`[]`, `{"nested":{}}`, `{"nested":[]}`, `{"n":1e400}`} {
		t.Run(raw, func(t *testing.T) {
			repo := &createKnowledgeFileRepoStub{}
			queue := &createKnowledgeTaskEnqueuerStub{}
			// A nil KB service also catches validation accidentally moving past a lookup.
			svc := &knowledgeService{repo: repo, task: queue}
			got, err := svc.CreateKnowledgeFromManual(context.Background(), "kb", &types.ManualKnowledgePayload{
				Content: "body", Status: "publish", CustomMetadata: json.RawMessage(raw),
			}, "")
			require.Nil(t, got)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			require.Zero(t, repo.createCalls)
			require.Zero(t, queue.calls)
		})
	}
}

type manualMetadataRepo struct {
	interfaces.KnowledgeRepository
	creates int
}

func (r *manualMetadataRepo) CreateKnowledge(ctx context.Context, k *types.Knowledge) error {
	r.creates++
	return r.KnowledgeRepository.CreateKnowledge(ctx, k)
}

func TestManualCustomMetadataPersistsOnCreate(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"", "draft", "publish"} {
		t.Run("status="+status, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			conn, err := db.DB()
			require.NoError(t, err)
			conn.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{}))
			repo := &manualMetadataRepo{KnowledgeRepository: repository.NewKnowledgeRepository(db)}
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
			kb.StorageProviderConfig = &types.StorageProviderConfig{Provider: "local"}
			queue := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo: repo, task: queue, kbService: &createKnowledgeFileKBServiceStub{kb: kb},
			}
			ctx := types.WithCaller(context.Background(), types.Caller{
				TenantID: 7, UserID: "editor", Role: types.TenantRoleAdmin,
			})
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7})
			ctx = (&access.KBAccess{
				KnowledgeBase: kb, Caller: types.CallerFromContext(ctx),
				EffectiveTenantID: 7, Permission: types.OrgRoleEditor,
			}).Context(ctx)
			for _, raw := range []string{
				"", "null", "{}",
				`{"source_id":"9007199254740993","raw_number":9007199254740993,"active":true,"parent":null}`,
			} {
				payload := &types.ManualKnowledgePayload{
					Title: "A document", Content: "Full body", Status: status,
				}
				if raw != "" {
					payload.CustomMetadata = json.RawMessage(raw)
				}
				created, err := svc.CreateKnowledgeFromManual(ctx, "kb", payload, "")
				require.NoError(t, err)
				stored, err := repo.GetKnowledgeByID(ctx, 7, created.ID)
				require.NoError(t, err)
				want := raw
				if want == "" || want == "null" {
					want = "{}"
				}
				require.Equal(t, want, string(created.CustomMetadata))
				require.Equal(t, want, string(stored.CustomMetadata), "persist raw JSON, not re-encoded float64")
				meta, err := stored.ManualMetadata()
				require.NoError(t, err)
				require.Equal(t, "Full body", meta.Content)
				require.NotContains(t, string(stored.Metadata), "source_id")
				require.Equal(t, "A document\nFull body", buildKnowledgeIndexContent(stored, meta.Content))
				if status == "publish" {
					require.Equal(t, "pending", stored.ParseStatus)
				} else {
					require.Equal(t, "draft", stored.ParseStatus)
				}
				// Body-only edits keep the attributes saved by the create request.
				updated, err := svc.UpdateManualKnowledge(ctx, created.ID, &types.ManualKnowledgePayload{
					Title: "A document", Content: "Updated body", Status: "draft",
				})
				require.NoError(t, err)
				require.Equal(t, want, string(updated.CustomMetadata))
				stored, err = repo.GetKnowledgeByID(ctx, 7, created.ID)
				require.NoError(t, err)
				require.Equal(t, want, string(stored.CustomMetadata))
			}
			require.Equal(t, 4, repo.creates)
			if status == "publish" {
				require.Equal(t, 4, queue.calls)
			} else {
				require.Zero(t, queue.calls)
			}
		})
	}
}

func TestUpdateKnowledgeCustomMetadataValidationRemainsCompatible(t *testing.T) {
	t.Parallel()
	fields := map[string]int{}
	for i := range 21 {
		fields[fmt.Sprintf("field-%d", i)] = i
	}
	excess, err := json.Marshal(fields)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"null is not normalized by the properties endpoint", "null", true},
		{"21 fields", string(excess), false},
		{"65 byte key", `{"` + strings.Repeat("k", 65) + `":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &types.Knowledge{
				ID: "doc", TenantID: 7, KnowledgeBaseID: "kb-1",
				CustomMetadata: types.JSON(`{"version":"old"}`),
			}
			repo := &metadataUpdateKnowledgeRepo{knowledge: original}
			svc := &knowledgeService{repo: repo, kbService: metadataUpdateKBService{}}
			ctx := types.WithCaller(context.Background(), types.Caller{
				TenantID: 7, UserID: "editor", Role: types.TenantRoleAdmin,
			})
			ctx = (&access.KBAccess{
				KnowledgeBase: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
				Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 7, Permission: types.OrgRoleEditor,
			}).Context(ctx)
			err := svc.UpdateKnowledge(ctx, &types.Knowledge{ID: "doc", CustomMetadata: types.JSON(tc.raw)})
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, tc.raw, string(repo.knowledge.CustomMetadata))
			} else {
				require.Error(t, err)
				require.Same(t, original, repo.knowledge, "invalid input must not reach repository.UpdateKnowledge")
				require.JSONEq(t, `{"version":"old"}`, string(repo.knowledge.CustomMetadata))
			}
		})
	}
}
