package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/semantic/cubeclient"
	"github.com/Tencent/WeKnora/internal/semantic/dbinspector"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Engine wires the module: repository + Cube client + file deployer.
type Engine struct {
	cfg      Config
	repo     *Repository
	client   *cubeclient.Client
	deployer *Deployer
}

// Config holds the module settings read from the environment (kept out of
// config.yaml on purpose so upstream files stay untouched).
type Config struct {
	// Enabled is the module master switch (CUBE_ENABLE).
	Enabled bool
	// APIURL is the Cube REST base, e.g. http://cube:4000/cubejs-api/v1.
	APIURL string
	// APISecret is CUBEJS_API_SECRET shared with the Cube container.
	APISecret string
	// ModelDir is the mounted Cube model directory (CUBE_MODEL_DIR).
	ModelDir string
	// DatasourcesFile is where driverFactory reads connection configs.
	DatasourcesFile string
	// MaxPreviewRows caps preview/query result rows (CUBE_MAX_PREVIEW_ROWS, default 200).
	MaxPreviewRows int
	// PublishTimeout bounds post-publish compile verification (CUBE_PUBLISH_TIMEOUT, default 30s).
	PublishTimeout time.Duration
	// HTTPTimeout is the Cube REST client timeout (CUBE_HTTP_TIMEOUT, default 120s).
	HTTPTimeout time.Duration
	// AuditLimit caps audit log list results (CUBE_AUDIT_LIMIT, default 100).
	AuditLimit int
}

// AdminGroup is granted on every published model and to platform admins'
// securityContext, keeping operator access independent of data groups.
const AdminGroup = "admin"

// NewEngine builds the module engine (nil client when not configured).
func NewEngine(cfg Config, db *gorm.DB) (*Engine, error) {
	e := &Engine{cfg: cfg, repo: NewRepository(db)}
	if cfg.APIURL != "" && cfg.APISecret != "" {
		e.client = cubeclient.New(cfg.APIURL, cfg.APISecret, cfg.HTTPTimeout)
	}
	var err error
	if e.deployer, err = NewDeployer(cfg.ModelDir, cfg.DatasourcesFile); err != nil {
		return nil, err
	}
	return e, nil
}

// Client exposes the Cube client for the agent tools.
func (e *Engine) Client() *cubeclient.Client { return e.client }

// Info reports module health for the studio header.
type Info struct {
	Enabled     bool     `json:"enabled"`
	CubeReady   bool     `json:"cube_ready"`
	CubeError   string   `json:"cube_error,omitempty"`
	GuidedTypes []string `json:"guided_types"`
}

// Info probes Cube reachability and lists guided connection types.
func (e *Engine) Info(ctx context.Context) Info {
	info := Info{
		Enabled:     e.cfg.Enabled,
		GuidedTypes: dbinspector.GuidedTypes(),
	}
	if e.client != nil {
		if err := e.client.Ping(ctx); err != nil {
			info.CubeError = err.Error()
		} else {
			info.CubeReady = true
		}
	} else {
		info.CubeError = "CUBE_API_URL / CUBEJS_API_SECRET not set"
	}
	return info
}

// ---- identity ----

// SecCtxFor builds the securityContext of a WeKnora user: their data groups
// plus the implicit admin group for platform admins.
func (e *Engine) SecCtxFor(ctx context.Context, tenantID uint64, userID string) (cubeclient.SecurityContext, error) {
	groups, err := e.repo.UserGroupNames(ctx, tenantID, userID)
	if err != nil {
		return cubeclient.SecurityContext{}, err
	}
	admin, err := e.repo.IsSystemAdmin(ctx, userID)
	if err != nil {
		// do not fail the request on the lookup; absence of admin grant is safe
		logger.Warnf(ctx, "[semantic] is_system_admin lookup failed: %v", err)
	}
	if admin {
		groups = append(groups, AdminGroup)
	}
	return cubeclient.SecurityContext{Sub: userID, Groups: groups, TenantID: tenantID}, nil
}

// WithUserContext returns ctx carrying the caller's securityContext.
func (e *Engine) WithUserContext(ctx context.Context, tenantID uint64, userID string) (context.Context, error) {
	sec, err := e.SecCtxFor(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return cubeclient.WithSecurityContext(ctx, sec), nil
}

// ---- audit ----

func (e *Engine) audit(
	ctx context.Context,
	tenantID uint64,
	userID, action, target string,
	detail map[string]interface{},
) {
	var raw types.JSON
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			raw = types.JSON(b)
		}
	}
	if err := e.repo.SaveAudit(ctx, &AuditLog{
		TenantID: tenantID, UserID: userID, Action: action, Target: target, Detail: raw,
	}); err != nil {
		logger.Warnf(ctx, "[semantic] audit write failed: %v", err)
	}
}

// ---- connection config crypto ----

// encryptConfig serializes the plaintext connection config with AES-256-GCM
// (same SYSTEM_AES_KEY scheme as data_sources). Empty key keeps plaintext —
// consistent with the platform's "no key configured" behaviour.
func encryptConfig(cfg *ConnectionConfig) (string, error) {
	if cfg == nil {
		return "", nil
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	if key := utils.GetAESKey(); key != nil {
		enc, err := utils.EncryptAESGCM(string(b), key)
		if err != nil {
			return "", err
		}
		return enc, nil
	}
	return string(b), nil
}

// decryptConfig is the inverse of encryptConfig. Decryption failures blank
// the config (row stays visible, UI shows "credentials not configured"),
// matching the platform's lenient read path.
func decryptConfig(conn *CubeConnection) *ConnectionConfig {
	if conn == nil || conn.ConfigEncrypted == "" {
		return &ConnectionConfig{}
	}
	raw := conn.ConfigEncrypted
	if key := utils.GetAESKey(); key != nil {
		if plain, err := utils.DecryptAESGCM(raw, key); err == nil {
			raw = plain
		} else {
			log.Printf("[semantic] connection %s decrypt failed, treating as unconfigured", conn.ID)
			return &ConnectionConfig{}
		}
	}
	var cfg ConnectionConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		log.Printf("[semantic] connection %s config parse failed: %v", conn.ID, err)
		return &ConnectionConfig{}
	}
	return &cfg
}

// ---- connections ----

// ConnectionInput is the create/update payload. Password empty on update
// means "keep the stored one".
type ConnectionInput struct {
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`
	Host        string                 `json:"host"`
	Port        int                    `json:"port"`
	Database    string                 `json:"database"`
	Username    string                 `json:"username"`
	Password    string                 `json:"password"`
	Extra       map[string]interface{} `json:"extra"`
}

func (in *ConnectionInput) toConfig() *ConnectionConfig {
	return &ConnectionConfig{
		Host: in.Host, Port: in.Port, Database: in.Database,
		Username: in.Username, Password: in.Password, Extra: in.Extra,
	}
}

// CreateConnection validates and persists a new connection, then syncs
// datasources.yaml so Cube can route to it immediately.
func (e *Engine) CreateConnection(
	ctx context.Context,
	userID string,
	tenant uint64,
	in *ConnectionInput,
) (*CubeConnection, error) {
	if !ValidSlug(in.Name) {
		return nil, fmt.Errorf("connection slug must match ^[a-z][a-z0-9_]*$")
	}
	if in.Type == "" {
		return nil, fmt.Errorf("database type is required")
	}
	if _, err := e.repo.FindConnectionByName(ctx, tenant, in.Name); err == nil {
		return nil, &ConflictError{Msg: fmt.Sprintf("connection slug %s already exists", in.Name)}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	enc, err := encryptConfig(in.toConfig())
	if err != nil {
		return nil, err
	}
	conn := &CubeConnection{
		TenantID: tenant, Name: in.Name, Title: in.Title, Description: in.Description,
		Type: in.Type, ConfigEncrypted: enc, Status: "active", CreatedBy: userID,
	}
	if err := e.repo.SaveConnection(ctx, conn); err != nil {
		return nil, err
	}
	if err := e.syncDatasources(ctx); err != nil {
		return nil, fmt.Errorf("connection saved, but syncing datasources.yaml failed: %w", err)
	}
	e.audit(ctx, tenant, userID, AuditConnectionCreate, "connection:"+in.Name, map[string]interface{}{"type": in.Type})
	return conn, nil
}

// UpdateConnection updates metadata/config; empty Password keeps the stored one.
func (e *Engine) UpdateConnection(
	ctx context.Context,
	userID string,
	tenant uint64,
	id string,
	in *ConnectionInput,
) (*CubeConnection, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if in.Title != "" {
		conn.Title = in.Title
	}
	if in.Description != "" {
		conn.Description = in.Description
	}
	if in.Type != "" && in.Type != conn.Type {
		if models, _ := e.repo.ListModels(ctx, tenant); len(models) > 0 {
			for _, m := range models {
				if m.ConnectionID == conn.ID && m.Status == ModelStatusPublished {
					return nil, fmt.Errorf("connection is used by a published model; " +
						"unpublish the relevant models before changing its type")
				}
			}
		}
		conn.Type = in.Type
	}
	cfg := decryptConfig(conn)
	if in.Host != "" {
		cfg.Host = in.Host
	}
	if in.Port != 0 {
		cfg.Port = in.Port
	}
	if in.Database != "" {
		cfg.Database = in.Database
	}
	if in.Username != "" {
		cfg.Username = in.Username
	}
	if in.Password != "" {
		cfg.Password = in.Password
	}
	if in.Extra != nil {
		cfg.Extra = in.Extra
	}
	enc, err := encryptConfig(cfg)
	if err != nil {
		return nil, err
	}
	conn.ConfigEncrypted = enc
	if err := e.repo.SaveConnection(ctx, conn); err != nil {
		return nil, err
	}
	if err := e.syncDatasources(ctx); err != nil {
		return nil, fmt.Errorf("connection saved, but syncing datasources.yaml failed: %w", err)
	}
	e.audit(ctx, tenant, userID, AuditConnectionUpdate, "connection:"+conn.Name, nil)
	return conn, nil
}

// DeleteConnection refuses to delete connections still referenced by models.
func (e *Engine) DeleteConnection(ctx context.Context, userID string, tenant uint64, id string) error {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return err
	}
	n, err := e.repo.CountModelsUsingConnection(ctx, conn.ID)
	if err != nil {
		return err
	}
	if n > 0 {
		return &ConflictError{Msg: fmt.Sprintf("connection is still referenced by %d model(s); delete or re-point those models first", n)}
	}
	if err := e.repo.DeleteConnection(ctx, conn); err != nil {
		return err
	}
	if err := e.syncDatasources(ctx); err != nil {
		return fmt.Errorf("connection deleted, but syncing datasources.yaml failed: %w", err)
	}
	e.audit(ctx, tenant, userID, AuditConnectionDelete, "connection:"+conn.Name, nil)
	return nil
}

// ListConnections returns the tenant's connections (credentials stripped by
// the DTO layer).
func (e *Engine) ListConnections(ctx context.Context, tenant uint64) ([]*CubeConnection, error) {
	return e.repo.ListConnections(ctx, tenant)
}

// GetConnection retrieves one connection.
func (e *Engine) GetConnection(ctx context.Context, tenant uint64, id string) (*CubeConnection, error) {
	return e.repo.FindConnection(ctx, tenant, id)
}

// TestConnectionRaw tests a not-yet-saved config straight from the request.
func (e *Engine) TestConnectionRaw(ctx context.Context, in *ConnectionInput) (map[string]interface{}, error) {
	cfg := in.toConfig()
	if in.Type == "" {
		return nil, fmt.Errorf("database type is required")
	}
	if _, ok := dbinspector.Get(in.Type); !ok {
		return nil, fmt.Errorf("type %s does not support guided connection testing; "+
			"for passthrough types, save it and verify through the publish flow", in.Type)
	}
	latency, err := dbinspector.TestConnection(ctx, in.Type, cfg)
	result := map[string]interface{}{"latency_ms": latency}
	if err != nil {
		result["ok"] = false
		result["error"] = err.Error()
		return result, err
	}
	result["ok"] = true
	return result, nil
}

// TestConnectionDraft validates unsaved edits: form params override the
// stored config, and an empty password falls back to the stored one. Lets
// the edit dialog "test connection" without re-entering credentials.
func (e *Engine) TestConnectionDraft(
	ctx context.Context,
	tenant uint64,
	id string,
	in *ConnectionInput,
) (map[string]interface{}, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if _, ok := dbinspector.Get(conn.Type); !ok {
		return nil, fmt.Errorf("type %s does not support guided connection testing", conn.Type)
	}
	cfg := decryptConfig(conn)
	if in.Host != "" {
		cfg.Host = in.Host
	}
	if in.Port != 0 {
		cfg.Port = in.Port
	}
	if in.Database != "" {
		cfg.Database = in.Database
	}
	if in.Username != "" {
		cfg.Username = in.Username
	}
	if in.Password != "" {
		cfg.Password = in.Password
	}
	if in.Extra != nil {
		cfg.Extra = in.Extra
	}
	latency, testErr := dbinspector.TestConnection(ctx, conn.Type, cfg)
	result := map[string]interface{}{"latency_ms": latency}
	if testErr != nil {
		result["ok"] = false
		result["error"] = testErr.Error()
		return result, testErr
	}
	result["ok"] = true
	return result, nil
}

// TestConnectionByID tests a saved connection and records the outcome.
func (e *Engine) TestConnectionByID(ctx context.Context, tenant uint64, id string) (map[string]interface{}, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	cfg := decryptConfig(conn)
	latency, testErr := dbinspector.TestConnection(ctx, conn.Type, cfg)
	result := map[string]interface{}{"latency_ms": latency}
	if testErr != nil {
		result["ok"] = false
		result["error"] = testErr.Error()
		conn.Status = "error"
	} else {
		result["ok"] = true
		conn.Status = "active"
	}
	raw, _ := json.Marshal(result)
	conn.LastTestResult = types.JSON(raw)
	if err := e.repo.SaveConnection(ctx, conn); err != nil {
		return result, err
	}
	return result, testErr
}

// syncDatasources rewrites datasources.yaml from all stored connections.
func (e *Engine) syncDatasources(ctx context.Context) error {
	conns, err := e.repo.AllConnections(ctx)
	if err != nil {
		return err
	}
	entries := make([]DatasourceEntry, 0, len(conns))
	for _, c := range conns {
		entries = append(entries, DatasourceEntry{
			ID: c.Name, Title: c.Title, Type: c.Type, Config: decryptConfig(c),
		})
	}
	return e.deployer.SyncDatasources(entries)
}

// ---- schema browsing / draft generation ----

// ListTables browses user tables of a guided connection.
func (e *Engine) ListTables(ctx context.Context, tenant uint64, id string) ([]dbinspector.TableRef, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	return dbinspector.ListTables(ctx, conn.Type, decryptConfig(conn))
}

// ListColumns inspects one table's columns.
func (e *Engine) ListColumns(
	ctx context.Context,
	tenant uint64,
	id, schema, table string,
) ([]dbinspector.ColumnSchema, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	return dbinspector.ListColumns(ctx, conn.Type, decryptConfig(conn), schema, table)
}

// DraftCube generates a starting-point cube YAML from an inspected table.
func (e *Engine) DraftCube(ctx context.Context, tenant uint64, id, schema, table string) (string, error) {
	conn, err := e.repo.FindConnection(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	// Validate the table exists in the browsed schema before generating SQL.
	tables, err := e.ListTables(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	found := false
	for _, tb := range tables {
		if tb.Schema == schema && tb.Name == table {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("table %s.%s not found in the connected database", schema, table)
	}
	cols, err := e.ListColumns(ctx, tenant, id, schema, table)
	if err != nil {
		return "", err
	}
	doc, err := BuildDraftCube(conn.Name, TableSchema{Schema: schema, Name: table, Columns: cols})
	if err != nil {
		return "", err
	}
	return GenerateModelYAML(doc)
}

// ---- models ----

// SaveModelInput carries editable fields; DraftYAML is the canonical body.
type SaveModelInput struct {
	Name             string   `json:"name"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	ConnectionID     string   `json:"connection_id"`
	Kind             string   `json:"kind"`
	DraftYAML        string   `json:"draft_yaml"`
	AllowedGroups    []string `json:"allowed_groups"`
	MemberVisibility string   `json:"member_visibility,omitempty"`
	ExpectedVersion  int      `json:"expected_version,omitempty"`
}

// validateModelYAML parses + validates a draft against published model names.
func (e *Engine) validateModelYAML(ctx context.Context, draftYAML string, selfName string) (*ModelDoc, error) {
	doc, err := ParseModelYAML(draftYAML)
	if err != nil {
		return nil, err
	}
	known, err := e.repo.AllModelNames(ctx, "")
	if err != nil {
		return nil, err
	}
	delete(known, selfName) // self is being replaced
	return doc, doc.Validate(KnownModelNames(known, doc))
}

// CreateModel persists a new draft.
func (e *Engine) CreateModel(
	ctx context.Context,
	userID string,
	tenant uint64,
	in *SaveModelInput,
) (*SemanticModel, error) {
	doc, err := e.validateModelYAML(ctx, in.DraftYAML, "")
	if err != nil {
		return nil, err
	}
	_, name, _ := doc.Identity()
	if in.ConnectionID != "" {
		if _, err := e.repo.FindConnection(ctx, tenant, in.ConnectionID); err != nil {
			return nil, fmt.Errorf("data source not found: %w", err)
		}
	}
	m := &SemanticModel{
		TenantID: tenant, Name: name,
		Title: in.Title, Description: in.Description,
		ConnectionID: in.ConnectionID, Kind: in.Kind,
		DraftYAML: in.DraftYAML, Status: ModelStatusDraft,
		AllowedGroups: StringListJSON(in.AllowedGroups), CreatedBy: userID,
		MemberVisibility: types.JSON(in.MemberVisibility),
	}
	if m.Kind == "" {
		m.Kind = ModelKindCube
	}
	if err := e.repo.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditModelCreate, "model:"+m.Name, nil)
	return m, nil
}

// UpdateModel updates metadata and draft YAML.
func (e *Engine) UpdateModel(
	ctx context.Context,
	userID string,
	tenant uint64,
	id string,
	in *SaveModelInput,
) (*SemanticModel, error) {
	m, err := e.repo.FindModel(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	// Optimistic locking: detect concurrent edits. The frontend sends the
	// version it loaded; if another user saved in between, the version
	// won't match and we return a conflict.
	if in.ExpectedVersion > 0 && m.Version != in.ExpectedVersion {
		return nil, &ConflictError{Msg: fmt.Sprintf(
			"model was modified by another user (current version: %d, expected: %d); please reload and retry",
			m.Version, in.ExpectedVersion,
		)}
	}
	// If DraftYAML is empty, keep the existing draft (metadata-only update).
	if in.DraftYAML == "" {
		in.DraftYAML = m.DraftYAML
	}
	doc, err := e.validateModelYAML(ctx, in.DraftYAML, m.Name)
	if err != nil {
		return nil, err
	}
	_, name, _ := doc.Identity()
	if name != m.Name && m.Status == ModelStatusPublished {
		return nil, &ConflictError{Msg: "model name cannot change while published; unpublish first"}
	}
	m.Name = name
	if in.Title != "" {
		m.Title = in.Title
	}
	if in.Description != "" {
		m.Description = in.Description
	}
	if in.ConnectionID != "" {
		if _, err := e.repo.FindConnection(ctx, tenant, in.ConnectionID); err != nil {
			return nil, fmt.Errorf("data source not found: %w", err)
		}
		m.ConnectionID = in.ConnectionID
	}
	if in.Kind != "" {
		m.Kind = in.Kind
	}
	m.DraftYAML = in.DraftYAML
	m.Version++
	if in.AllowedGroups != nil {
		m.AllowedGroups = StringListJSON(in.AllowedGroups)
	}
	if in.MemberVisibility != "" {
		m.MemberVisibility = types.JSON(in.MemberVisibility)
	}
	if err := e.repo.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditModelUpdate, "model:"+m.Name, nil)
	return m, nil
}

// DeleteModel removes the model and its published file.
func (e *Engine) DeleteModel(ctx context.Context, userID string, tenant uint64, id string) error {
	m, err := e.repo.FindModel(ctx, tenant, id)
	if err != nil {
		return err
	}
	if !e.canManageModel(ctx, m, userID) {
		return &ConflictError{Msg: "only the creator or an admin can delete this model"}
	}
	if m.Status == ModelStatusPublished {
		if err := e.deployer.UnpublishModel(m.TenantID, m.Name); err != nil {
			return fmt.Errorf("failed to remove published file: %w", err)
		}
	}
	if err := e.repo.DeleteModel(ctx, m); err != nil {
		return err
	}
	e.audit(ctx, tenant, userID, AuditModelDelete, "model:"+m.Name, nil)
	return nil
}

// ListModels lists the tenant's models.
func (e *Engine) ListModels(ctx context.Context, tenant uint64) ([]*SemanticModel, error) {
	return e.repo.ListModels(ctx, tenant)
}

// GetModel retrieves one model.
func (e *Engine) GetModel(ctx context.Context, tenant uint64, id string) (*SemanticModel, error) {
	return e.repo.FindModel(ctx, tenant, id)
}

// PublishResult reports the publish outcome.
type PublishResult struct {
	Status  string `json:"status"`
	Version int    `json:"version"`
	Error   string `json:"error,omitempty"`
}

// Publish writes the draft YAML to the shared model volume, waits for Cube
// to compile it, then snapshots a version. A failed compile removes the
// file so the Cube deployment never serves from a broken schema.
func (e *Engine) Publish(
	ctx context.Context,
	userID string,
	tenant uint64,
	id string,
	note string,
) (*PublishResult, error) {
	publishMu.Lock()
	defer publishMu.Unlock()
	m, err := e.repo.FindModel(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if m.ConnectionID == "" {
		return nil, fmt.Errorf("model is not bound to a data source; pick one in the editor first")
	}
	if _, err := e.repo.FindConnection(ctx, tenant, m.ConnectionID); err != nil {
		return nil, fmt.Errorf("bound data source not found: %w", err)
	}
	doc, err := ParseModelYAML(m.DraftYAML)
	if err != nil {
		e.markPublishFailed(ctx, m, err.Error())
		return nil, err
	}
	// join target validation: the target model just needs to exist (including
	// unpublished ones) — interrelated model pairs must be publishable in any
	// order; compile-time problems are caught later by post-publish /v1/meta polling
	otherNames, err := e.repo.AllModelNames(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(KnownModelNames(otherNames, doc)); err != nil {
		e.markPublishFailed(ctx, m, err.Error())
		return nil, err
	}
	// Force-override data_source with the bound connection slug.
	// Prevents a contributor from writing another tenant's connection slug
	// in YAML to query cross-tenant databases.
	boundConn, err := e.repo.FindConnection(ctx, tenant, m.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("bound data source not found: %w", err)
	}
	kind, name, idErr := doc.Identity()
	if idErr != nil {
		return nil, idErr
	}
	switch kind {
	case ModelKindCube:
		// Force data_source to the bound connection slug.
		doc.Cubes[0].DataSource = boundConn.Name
		// Force-inject access_policy with member-level visibility if configured.
		groups := StringList(m.AllowedGroups)
		vis := parseMemberVisibility(m.MemberVisibility)
		doc.Cubes[0].AccessPolicy = BuildPolicyWithVisibility(vis, groups)
	case ModelKindView:
		// Views also get forced access_policy (Cube member-level rules
		// on the underlying cube do NOT cascade to views).
		if len(doc.Views) > 0 {
			doc.Views[0].Extra = setViewPolicy(doc.Views[0].Extra, BuildPolicy(StringList(m.AllowedGroups)))
		}
	}
	yamlText, err := GenerateModelYAML(doc)
	if err != nil {
		return nil, err
	}
	if err := e.syncDatasources(ctx); err != nil {
		return nil, fmt.Errorf("failed to sync datasources.yaml: %w", err)
	}
	if err := e.deployer.PublishModel(m.TenantID, name, yamlText); err != nil {
		e.markPublishFailed(ctx, m, err.Error())
		return nil, err
	}
	pubCtx, cancel := context.WithTimeout(ctx, e.cfg.PublishTimeout)
	defer cancel()
	if e.client == nil {
		e.markPublishFailed(ctx, m, "cube client not configured")
		return nil, fmt.Errorf("cube client not configured (CUBE_API_URL / CUBEJS_API_SECRET)")
	}
	// Wait for the model to appear in /v1/meta. Additionally verify that the
	// measure count matches the new definition (catches stale schema from a
	// failed re-publish of an existing model).
	expectedMeasures := 0
	if len(doc.Cubes) > 0 {
		expectedMeasures = len(doc.Cubes[0].Measures)
	}
	verifyFn := func(meta *cubeclient.MetaResponse) bool {
		for _, cb := range meta.Cubes {
			if cb.Name == name {
				return len(cb.Measures) == expectedMeasures
			}
		}
		return false
	}
	if err := e.client.WaitUntilCompiledVerify(pubCtx, name, verifyFn, e.cfg.PublishTimeout); err != nil {
		_ = e.deployer.UnpublishModel(m.TenantID, name) // self-heal: do not serve a broken schema
		e.markPublishFailed(ctx, m, err.Error())
		return nil, err
	}
	next := m.Version + 1
	now := time.Now().UTC()
	m.Status = ModelStatusPublished
	m.PublishedYAML = yamlText
	m.LastError = ""
	m.Version = next
	m.PublishedAt = &now
	if err := e.repo.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	if err := e.repo.SaveModelVersion(ctx, &SemanticModelVersion{
		TenantID: tenant, ModelID: m.ID, Version: next, YAML: yamlText,
		AllowedGroups: m.AllowedGroups, MemberVisibility: m.MemberVisibility, Note: note, PublishedBy: userID, PublishedAt: now,
	}); err != nil {
		logger.Warnf(ctx, "[semantic] version snapshot failed: %v", err)
	}
	e.audit(ctx, tenant, userID, AuditModelPublish, "model:"+m.Name, map[string]interface{}{"version": next})
	return &PublishResult{Status: m.Status, Version: next}, nil
}

// canManageModel reports whether the user can perform destructive operations
// (publish / unpublish / delete / rollback) on a model. The creator always
// can; tenant admins and system admins can manage anyone's models.
// Matches the KB OwnedKBOrAdmin pattern.
func (e *Engine) canManageModel(ctx context.Context, m *SemanticModel, userID string) bool {
	if m.CreatedBy == userID {
		return true
	}
	// Tenant admins (owner/admin) can manage all models in the tenant.
	isTenantAdmin, err := e.repo.IsTenantAdmin(ctx, m.TenantID, userID)
	if err == nil && isTenantAdmin {
		return true
	}
	// System admins can manage models across all tenants.
	isSysAdmin, err := e.repo.IsSystemAdmin(ctx, userID)
	if err != nil {
		return false
	}
	return isSysAdmin
}

func (e *Engine) markPublishFailed(ctx context.Context, m *SemanticModel, msg string) {
	m.Status = ModelStatusPublishFailed
	m.LastError = msg
	if err := e.repo.SaveModel(ctx, m); err != nil {
		logger.Warnf(ctx, "[semantic] markPublishFailed: %v", err)
	}
}

// Unpublish removes the model file; the draft and history remain.
func (e *Engine) Unpublish(ctx context.Context, userID string, tenant uint64, id string) error {
	m, err := e.repo.FindModel(ctx, tenant, id)
	if err != nil {
		return err
	}
	if err := e.deployer.UnpublishModel(m.TenantID, m.Name); err != nil {
		return err
	}
	m.Status = ModelStatusDraft
	m.PublishedYAML = ""
	if err := e.repo.SaveModel(ctx, m); err != nil {
		return err
	}
	e.audit(ctx, tenant, userID, AuditModelUnpublish, "model:"+m.Name, nil)
	return nil
}

// ListVersions returns publish snapshots.
func (e *Engine) ListVersions(ctx context.Context, tenant uint64, modelID string) ([]*SemanticModelVersion, error) {
	return e.repo.ListModelVersions(ctx, tenant, modelID)
}

// Rollback republishes a historical snapshot.
func (e *Engine) Rollback(
	ctx context.Context,
	userID string,
	tenant uint64,
	modelID, versionID string,
) (*PublishResult, error) {
	m, err := e.repo.FindModel(ctx, tenant, modelID)
	if err != nil {
		return nil, err
	}
	v, err := e.repo.FindModelVersion(ctx, tenant, versionID)
	if err != nil {
		return nil, err
	}
	if v.ModelID != m.ID {
		return nil, fmt.Errorf("version does not match the model")
	}
	if !e.canManageModel(ctx, m, userID) {
		return nil, &ConflictError{Msg: "only the creator or an admin can roll back this model"}
	}
	m.DraftYAML = v.YAML
	m.AllowedGroups = v.AllowedGroups
	m.MemberVisibility = v.MemberVisibility
	if err := e.repo.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	res, err := e.Publish(ctx, userID, tenant, modelID, "rollback to v"+fmt.Sprint(v.Version))
	if err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditModelRollback, "model:"+m.Name, map[string]interface{}{"to_version": v.Version})
	return res, nil
}

// ---- query paths (preview + agent tools) ----

// MetaForUser returns /v1/meta filtered to what the caller's groups can see.
// Dev-mode Cube leaks invisible members, so the local filter re-applies the
// published accessPolicy decisions.
func (e *Engine) MetaForUser(ctx context.Context, tenantID uint64, userID string) (*cubeclient.MetaResponse, error) {
	uctx, err := e.WithUserContext(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	meta, err := e.client.Meta(uctx)
	if err != nil {
		return nil, err
	}
	// filtering depends on the caller identity, so pass the uctx that
	// carries the securityContext
	return e.filterMetaForUser(uctx, tenantID, userID, meta), nil
}

// filterMetaForUser drops models whose accessPolicy excludes the caller.
func (e *Engine) filterMetaForUser(
	ctx context.Context,
	_ uint64, // tenantID: unused (identity from ctx)
	_ string, // userID: unused (identity from ctx)
	meta *cubeclient.MetaResponse,
) *cubeclient.MetaResponse {
	sec := cubeclient.SecurityContextFromContext(ctx)
	allowed := make(map[string]bool, len(sec.Groups))
	for _, g := range sec.Groups {
		allowed[g] = true
	}
	isAdmin := allowed[AdminGroup]
	out := make([]cubeclient.MetaCube, 0, len(meta.Cubes))
	for _, cb := range meta.Cubes {
		if isAdmin || e.modelAllowsGroup(ctx, cb.Name, allowed) {
			out = append(out, cb)
		}
	}
	meta.Cubes = out
	return meta
}

// modelAllowsGroup resolves a published model's group policy from the DB.
func (e *Engine) modelAllowsGroup(ctx context.Context, modelName string, groups map[string]bool) bool {
	var m SemanticModel
	err := e.repo.db.WithContext(ctx).
		Where("name = ? AND status = ?", modelName, ModelStatusPublished).
		First(&m).Error
	if err != nil {
		return false // unpublished names must not surface
	}
	for _, g := range StringList(m.AllowedGroups) {
		if groups[g] {
			return true
		}
	}
	return false
}

// PreviewQuery is the studio preview / agent cube_query payload.
type PreviewQuery struct {
	Measures       []string          `json:"measures"`
	Dimensions     []string          `json:"dimensions"`
	TimeDimensions []TimeDimension   `json:"time_dimensions,omitempty"`
	Filters        []QueryFilter     `json:"filters,omitempty"`
	Order          map[string]string `json:"order,omitempty"`
	Limit          int               `json:"limit,omitempty"`
	Offset         int               `json:"offset,omitempty"`
	Timezone       string            `json:"timezone,omitempty"`
}

// TimeDimension is one timeDimensions entry.
type TimeDimension struct {
	Dimension   string   `json:"dimension"`
	DateRange   []string `json:"date_range,omitempty"`
	Granularity string   `json:"granularity,omitempty"`
}

// QueryFilter is one filter entry.
type QueryFilter struct {
	Member   string        `json:"member"`
	Operator string        `json:"operator"`
	Values   []string      `json:"values,omitempty"`
	And      []QueryFilter `json:"and,omitempty"`
	Or       []QueryFilter `json:"or,omitempty"`
}

// toCubeQuery converts the typed query into the Cube query JSON.
func (q *PreviewQuery) toCubeQuery(maxRows int) cubeclient.Query {
	if q.Limit <= 0 || q.Limit > maxRows {
		q.Limit = maxRows
	}
	tds := make([]map[string]interface{}, 0, len(q.TimeDimensions))
	for _, td := range q.TimeDimensions {
		entry := map[string]interface{}{"dimension": td.Dimension}
		if len(td.DateRange) > 0 {
			entry["dateRange"] = td.DateRange
		}
		if td.Granularity != "" {
			entry["granularity"] = td.Granularity
		}
		tds = append(tds, entry)
	}
	filters := make([]map[string]interface{}, 0, len(q.Filters))
	for _, f := range q.Filters {
		filters = append(filters, filterToCube(f))
	}
	cq := cubeclient.Query{}
	if len(q.Measures) > 0 {
		cq["measures"] = q.Measures
	}
	if len(q.Dimensions) > 0 {
		cq["dimensions"] = q.Dimensions
	}
	if len(tds) > 0 {
		cq["timeDimensions"] = tds
	}
	if len(filters) > 0 {
		cq["filters"] = filters
	}
	if len(q.Order) > 0 {
		cq["order"] = q.Order
	}
	cq["limit"] = q.Limit
	if q.Offset > 0 {
		cq["offset"] = q.Offset
	}
	if q.Timezone != "" {
		cq["timezone"] = q.Timezone
	}
	return cq
}

func filterToCube(f QueryFilter) map[string]interface{} {
	if len(f.And) > 0 || len(f.Or) > 0 {
		out := map[string]interface{}{}
		if len(f.And) > 0 {
			members := make([]map[string]interface{}, 0, len(f.And))
			for _, sub := range f.And {
				members = append(members, filterToCube(sub))
			}
			out["and"] = members
		}
		if len(f.Or) > 0 {
			members := make([]map[string]interface{}, 0, len(f.Or))
			for _, sub := range f.Or {
				members = append(members, filterToCube(sub))
			}
			out["or"] = members
		}
		return out
	}
	entry := map[string]interface{}{"member": f.Member, "operator": f.Operator}
	if len(f.Values) > 0 {
		entry["values"] = f.Values
	}
	return entry
}

// Preview runs a query under the caller's identity. Only published models
// exist in Cube, so the model must be published first.
func (e *Engine) Preview(
	ctx context.Context,
	userID string,
	tenant uint64,
	modelID string,
	q *PreviewQuery,
) (*cubeclient.LoadResponse, error) {
	m, err := e.repo.FindModel(ctx, tenant, modelID)
	if err != nil {
		return nil, err
	}
	if m.Status != ModelStatusPublished {
		return nil, fmt.Errorf("model is not published; publish it before previewing")
	}
	uctx, err := e.WithUserContext(ctx, tenant, userID)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Load(uctx, q.toCubeQuery(e.cfg.MaxPreviewRows))
	if err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditModelQuery, "model:"+m.Name,
		map[string]interface{}{"rows": len(resp.Data), "measures": q.Measures, "dimensions": q.Dimensions})
	return resp, nil
}

// QueryForUser executes a Cube query under the caller's identity (agent tools).
func (e *Engine) QueryForUser(
	ctx context.Context,
	tenantID uint64,
	userID string,
	q *PreviewQuery,
) (*cubeclient.LoadResponse, error) {
	uctx, err := e.WithUserContext(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Load(uctx, q.toCubeQuery(e.cfg.MaxPreviewRows))
	if err != nil {
		return nil, err
	}
	e.audit(ctx, tenantID, userID, AuditModelQuery, "model:query",
		map[string]interface{}{"rows": len(resp.Data), "measures": q.Measures, "dimensions": q.Dimensions})
	// Attach the generated SQL for transparency (dry-run, no extra DB cost).
	if sqlResp, sqlErr := e.client.SQL(uctx, q.toCubeQuery(e.cfg.MaxPreviewRows)); sqlErr == nil && len(sqlResp.SQL) > 0 { //nolint:lll // long but readable
		resp.GeneratedSQL = sqlResp.SQL
	}
	return resp, nil
}

// SQLForUser dry-runs a query under the caller's identity (agent tools).
func (e *Engine) SQLForUser(
	ctx context.Context,
	tenantID uint64,
	userID string,
	q *PreviewQuery,
) (*cubeclient.SQLResponse, error) {
	uctx, err := e.WithUserContext(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return e.client.SQL(uctx, q.toCubeQuery(e.cfg.MaxPreviewRows))
}

// ---- data groups ----

// CreateGroup creates a data group.
func (e *Engine) CreateGroup(ctx context.Context, userID string, tenant uint64, g *DataGroup) (*DataGroup, error) {
	if !ValidSlug(g.Name) {
		return nil, fmt.Errorf("group name must match ^[a-z][a-z0-9_]*$")
	}
	g.TenantID = tenant
	g.CreatedBy = userID
	if err := e.repo.SaveGroup(ctx, g); err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditGroupCreate, "group:"+g.Name, nil)
	return g, nil
}

// UpdateGroup updates group metadata.
func (e *Engine) UpdateGroup(
	ctx context.Context,
	userID string,
	tenant uint64,
	id string,
	in *DataGroup,
) (*DataGroup, error) {
	g, err := e.repo.FindGroup(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if in.Title != "" {
		g.Title = in.Title
	}
	g.Description = in.Description
	if err := e.repo.SaveGroup(ctx, g); err != nil {
		return nil, err
	}
	e.audit(ctx, tenant, userID, AuditGroupUpdate, "group:"+g.Name, nil)
	return g, nil
}

// GroupUsage reports how many models reference a data group slug, so the
// UI can warn before deleting (stale slugs persist until republish).
type GroupUsage struct {
	ModelCount int      `json:"model_count"`
	ModelNames []string `json:"model_names"`
}

// GetGroupUsage computes the referencing models of one group.
func (e *Engine) GetGroupUsage(ctx context.Context, tenant uint64, groupID string) (*GroupUsage, error) {
	g, err := e.repo.FindGroup(ctx, tenant, groupID)
	if err != nil {
		return nil, err
	}
	models, err := e.repo.ListModels(ctx, tenant)
	if err != nil {
		return nil, err
	}
	usage := &GroupUsage{ModelNames: []string{}}
	for _, m := range models {
		for _, slug := range StringList(m.AllowedGroups) {
			if slug == g.Name {
				usage.ModelCount++
				usage.ModelNames = append(usage.ModelNames, m.Name)
				break
			}
		}
	}
	return usage, nil
}

// DeleteGroup removes a group. Models referencing it keep working with the
// stale slug until republished — surfaced in the UI via group usage check.
func (e *Engine) DeleteGroup(ctx context.Context, userID string, tenant uint64, id string) error {
	g, err := e.repo.FindGroup(ctx, tenant, id)
	if err != nil {
		return err
	}
	if err := e.repo.DeleteGroup(ctx, g); err != nil {
		return err
	}
	e.audit(ctx, tenant, userID, AuditGroupDelete, "group:"+g.Name, nil)
	return nil
}

// ListGroups lists the tenant's groups.
func (e *Engine) ListGroups(ctx context.Context, tenant uint64) ([]*DataGroup, error) {
	return e.repo.ListGroups(ctx, tenant)
}

// SetGroupMembers rewrites a group's member list.
func (e *Engine) SetGroupMembers(
	ctx context.Context,
	userID string,
	tenant uint64,
	groupID string,
	userIDs []string,
) error {
	g, err := e.repo.FindGroup(ctx, tenant, groupID)
	if err != nil {
		return err
	}
	if err := e.repo.ReplaceGroupMembers(ctx, tenant, g.ID, userIDs); err != nil {
		return err
	}
	e.audit(ctx, tenant, userID, AuditGroupMembers, "group:"+g.Name, map[string]interface{}{"members": len(userIDs)})
	return nil
}

// ListGroupMembers lists one group's members.
func (e *Engine) ListGroupMembers(ctx context.Context, tenant uint64, groupID string) ([]*DataGroupMember, error) {
	return e.repo.ListGroupMembers(ctx, tenant, groupID)
}

// ListAudits lists recent module audit records.
func (e *Engine) ListAudits(ctx context.Context, tenant uint64, limit int) ([]*AuditLog, error) {
	return e.repo.ListAudits(ctx, tenant, limit)
}

// WarnDenied inspects an empty load result and returns a hint when the
// securityContext has no access to the requested members (mirrors the
// cube-mcp rlsAccessDenied detection: Cube silently returns empty rows).
func WarnDenied(meta *cubeclient.MetaResponse, q *PreviewQuery) string {
	if meta == nil || len(meta.Cubes) == 0 {
		return "the current identity has no visible models; contact an admin to configure data groups"
	}
	visible := map[string]bool{}
	for _, cb := range meta.Cubes {
		visible[cb.Name] = true
	}
	var requested []string
	for _, m := range append(append([]string{}, q.Measures...), q.Dimensions...) {
		if i := strings.Index(m, "."); i > 0 {
			requested = append(requested, m[:i])
		}
	}
	for _, name := range requested {
		if !visible[name] {
			return fmt.Sprintf("permission denied: the current identity cannot access model %s", name)
		}
	}
	return ""
}

// parseMemberVisibility decodes the MemberVisibility JSONB field into a
// MemberVisibility map. Returns empty map on parse failure.
func parseMemberVisibility(raw types.JSON) MemberVisibility {
	if len(raw) == 0 {
		return MemberVisibility{}
	}
	var vis MemberVisibility
	if err := json.Unmarshal(raw, &vis); err != nil {
		return MemberVisibility{}
	}
	return vis
}

// setViewPolicy injects or replaces the access_policy in a view's Extra map.
func setViewPolicy(
	extra map[string]interface{},
	rules []PolicyRule,
) map[string]interface{} {
	if extra == nil {
		extra = map[string]interface{}{}
	}
	extra["access_policy"] = rules
	return extra
}

// publishMu serializes publishes to prevent concurrent publishes from
// corrupting each other (e.g. A publishes while B writes a broken model,
// causing the whole schema to fail compilation and A to be falsely marked failed).
var publishMu sync.Mutex

// canManageModel reports whether the user can perform destructive operations
// (publish / unpublish / delete / rollback) on a model. The creator always
// can; admins can manage anyone's models. Matches the KB OwnedKBOrAdmin pattern.
func canManageModel(model *SemanticModel, userID string, isAdmin bool) bool {
	return model.CreatedBy == userID || isAdmin
}

// canEditModel reports whether the user can modify a model's draft.
// Contributors can edit any model (same as upstream KB edit-on-create pattern
// for draft collaboration), but destructive ops require canManageModel.
func canEditModel(_ *SemanticModel, _ string, _ bool) bool {
	return true // any contributor can edit drafts; per-model ownership checked at delete/publish
}
