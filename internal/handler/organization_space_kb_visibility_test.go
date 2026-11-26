package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// spaceVisibilityDDL mirrors the tables the per-space KB listing reads, taken
// from migrations/sqlite/000000_init.up.sql (plus the knowledge_bases DDL kept
// in sync with repository/knowledgebase_sqlite_test.go).
const spaceVisibilityDDL = `
CREATE TABLE IF NOT EXISTS knowledge_bases (
    profile_config TEXT,
    generated_profile TEXT,
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    chunking_config TEXT NOT NULL DEFAULT '{}',
    image_processing_config TEXT NOT NULL DEFAULT '{}',
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    cos_config TEXT NOT NULL DEFAULT '{}',
    storage_provider_config TEXT DEFAULT NULL,
    vlm_config TEXT NOT NULL DEFAULT '{}',
    extract_config TEXT NULL DEFAULT NULL,
    faq_config TEXT,
    question_generation_config TEXT NULL,
    auto_tag_config TEXT NULL,
    is_temporary BOOLEAN NOT NULL DEFAULT 0,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    pinned_at DATETIME NULL,
    asr_config TEXT,
    vector_store_id VARCHAR(36),
    storage_backend_id VARCHAR(36),
    wiki_config TEXT,
    indexing_strategy TEXT,
    creator_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id INTEGER NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT 0,
    invite_code_expires_at DATETIME,
    invite_code_validity_days SMALLINT NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT 0,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS organization_tenant_members (
    id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'viewer',
    representative_user_id VARCHAR(36) NOT NULL DEFAULT '',
    joined_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS kb_shares (
    id VARCHAR(36) PRIMARY KEY,
    knowledge_base_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
    runnable_by_viewer BOOLEAN NOT NULL DEFAULT 1,
    config TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    PRIMARY KEY (id, tenant_id)
);
CREATE TABLE IF NOT EXISTS agent_shares (
    id VARCHAR(36) PRIMARY KEY,
    agent_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS tenant_disabled_shared_agents (
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    source_tenant_id BIGINT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, agent_id, source_tenant_id)
);
`

// Minimal count fakes: the per-space listing decorates cards with document /
// chunk counts; the values are irrelevant to visibility.
type spaceVisibilityKGRepo struct{ interfaces.KnowledgeRepository }

func (spaceVisibilityKGRepo) CountKnowledgeByKnowledgeBaseID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}

type spaceVisibilityChunkRepo struct{ interfaces.ChunkRepository }

func (spaceVisibilityChunkRepo) CountChunksByKnowledgeBaseID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}

// spaceVisibilityKBSvc adapts the real KB repository to the two read methods
// the handler uses when merging agent-carried KBs.
type spaceVisibilityKBSvc struct {
	interfaces.KnowledgeBaseService
	repo interfaces.KnowledgeBaseRepository
}

func (s spaceVisibilityKBSvc) GetKnowledgeBaseByIDOnly(ctx context.Context, kbID string) (*types.KnowledgeBase, error) {
	return s.repo.GetKnowledgeBaseByID(ctx, kbID)
}

func (s spaceVisibilityKBSvc) ListKnowledgeBasesByTenantID(
	ctx context.Context, tenantID uint64,
) ([]*types.KnowledgeBase, error) {
	return s.repo.ListKnowledgeBasesByTenantID(ctx, tenantID)
}

type spaceVisibilityOrgSvc struct {
	interfaces.OrganizationService
	repo interfaces.OrganizationRepository
}

func (s spaceVisibilityOrgSvc) GetOrganization(ctx context.Context, orgID string) (*types.Organization, error) {
	return s.repo.GetByID(ctx, orgID)
}

type spaceVisibilityEnv struct {
	t  *testing.T
	db *gorm.DB
	h  *OrganizationHandler
}

const (
	spaceTenantA   = uint64(101)
	spaceTenantB   = uint64(202)
	spaceOrgID     = "org-space-vis-1"
	spaceKBShared  = "kb-shared-1"
	spaceKBPrivate = "kb-private-1"
	spaceAgentID   = "agent-a-1"
)

func newSpaceVisibilityEnv(t *testing.T) *spaceVisibilityEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(spaceVisibilityDDL).Error)

	orgRepo := repository.NewOrganizationRepository(db)
	kbRepo := repository.NewKnowledgeBaseRepository(db)
	kgRepo := spaceVisibilityKGRepo{}
	chunkRepo := spaceVisibilityChunkRepo{}

	kbShareSvc := service.NewKBShareService(
		repository.NewKBShareRepository(db), orgRepo, kbRepo, kgRepo, chunkRepo, nil,
	)
	agentShareSvc := service.NewAgentShareService(
		repository.NewAgentShareRepository(db),
		repository.NewTenantDisabledSharedAgentRepository(db),
		orgRepo,
		repository.NewCustomAgentRepository(db),
		nil, nil, kbRepo,
	)

	h := &OrganizationHandler{
		shareService:      kbShareSvc,
		agentShareService: agentShareSvc,
		orgService:        spaceVisibilityOrgSvc{repo: orgRepo},
		kbService:         spaceVisibilityKBSvc{repo: kbRepo},
		knowledgeRepo:     kgRepo,
		chunkRepo:         chunkRepo,
	}
	return &spaceVisibilityEnv{t: t, db: db, h: h}
}

// seedSpace lays out issue #5651's shape: workspace A owns a shared space,
// shared exactly one of its two KBs into it, and workspace B joined the space.
func (e *spaceVisibilityEnv) seedSpace() {
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO organizations (id, name, owner_id, owner_tenant_id, searchable) VALUES (?, ?, ?, ?, 1)`,
		spaceOrgID, "A shared space", "user-a", spaceTenantA,
	).Error)
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO organization_tenant_members (id, organization_id, tenant_id, role, representative_user_id) `+
			`VALUES (?, ?, ?, 'admin', 'user-a')`,
		"member-a", spaceOrgID, spaceTenantA,
	).Error)
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO organization_tenant_members (id, organization_id, tenant_id, role, representative_user_id) `+
			`VALUES (?, ?, ?, 'editor', 'user-b')`,
		"member-b", spaceOrgID, spaceTenantB,
	).Error)
	for _, kb := range []struct {
		id   string
		name string
	}{
		{spaceKBShared, "shared KB"},
		{spaceKBPrivate, "private KB"},
	} {
		require.NoError(e.t, e.db.Exec(
			`INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id, creator_id)
			 VALUES (?, ?, ?, 'embed', 'summary', 'user-a')`,
			kb.id, kb.name, spaceTenantA,
		).Error)
	}
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO kb_shares (id, knowledge_base_id, organization_id, shared_by_user_id, source_tenant_id, permission)
		 VALUES ('share-1', ?, ?, 'user-a', ?, 'viewer')`,
		spaceKBShared, spaceOrgID, spaceTenantA,
	).Error)
}

func (e *spaceVisibilityEnv) seedSharedAgent(mode string, kbIDs string) {
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO custom_agents (id, name, tenant_id, created_by, config, runnable_by_viewer)
		 VALUES (?, 'A assistant', ?, 'user-a', ?, 1)`,
		spaceAgentID, spaceTenantA,
		`{"kb_selection_mode":"`+mode+`","knowledge_bases":[`+kbIDs+`]}`,
	).Error)
	require.NoError(e.t, e.db.Exec(
		`INSERT INTO agent_shares (id, agent_id, organization_id, shared_by_user_id, source_tenant_id, permission)
		 VALUES ('agent-share-1', ?, ?, 'user-a', ?, 'viewer')`,
		spaceAgentID, spaceOrgID, spaceTenantA,
	).Error)
}

func (e *spaceVisibilityEnv) listAsB() []map[string]interface{} {
	e.t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleOwner)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	c.Set(types.TenantIDContextKey.String(), spaceTenantB)
	c.Params = gin.Params{{Key: "id", Value: spaceOrgID}}

	e.h.ListOrganizationSharedKnowledgeBases(c)
	require.Empty(e.t, c.Errors)
	require.Equal(e.t, http.StatusOK, rec.Code)

	var body struct {
		Success bool                     `json:"success"`
		Data    []map[string]interface{} `json:"data"`
	}
	require.NoError(e.t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(e.t, body.Success)
	return body.Data
}

// Regression for #5651: a member of a shared space must see only the KBs the
// owning workspace actually shared into that space — not the owner's other
// knowledge bases.
func TestSpaceViewListsOnlyDirectlySharedKnowledgeBases(t *testing.T) {
	env := newSpaceVisibilityEnv(t)
	env.seedSpace()

	rows := env.listAsB()
	require.Len(t, rows, 1, "member must see exactly the one shared KB, got %v", rowKBIDs(rows))
	require.Equal(t, []string{spaceKBShared}, rowKBIDs(rows))
}

// Sharing an agent into the space also lists the KBs its scope carries
// (documented agent-share semantics; see website-docs/04-api/02-api-org.md),
// including KBs the owner never shared directly. This pins the current
// behavior so a later policy change has to touch this test deliberately.
func TestSpaceViewMergesAgentCarriedKnowledgeBases(t *testing.T) {
	env := newSpaceVisibilityEnv(t)
	env.seedSpace()
	env.seedSharedAgent("all", "")

	rows := env.listAsB()
	require.ElementsMatch(t, []string{spaceKBShared, spaceKBPrivate}, rowKBIDs(rows))
}

func rowKBIDs(rows []map[string]interface{}) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		kb, _ := row["knowledge_base"].(map[string]interface{})
		if id, ok := kb["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}
