package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSkillTestRepo(t *testing.T) TenantSkillRepository {
	t.Helper()
	repo, _ := newSkillTestRepoWithDB(t)
	return repo
}

// newSkillTestRepoWithDB also hands back the handle, for the tests that must
// read a column raw to prove a secret is encrypted at rest.
func newSkillTestRepoWithDB(t *testing.T) (TenantSkillRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillSnapshotEntity{},
		&types.TenantUserEnvVar{}, &types.TenantSkillCatalogEntity{},
	))
	// AutoMigrate cannot express the partial unique index, so add it here to
	// match the production migration.
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skills_config_name
		 ON tenant_skills (sandbox_config_id, name) WHERE deleted_at IS NULL`).Error)
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skill_catalog_name
		 ON tenant_skill_catalog (tenant_id, name) WHERE deleted_at IS NULL`).Error)
	return NewTenantSkillRepository(db), db
}

func skillRow(id, configID, name string) *types.TenantSkillEntity {
	return &types.TenantSkillEntity{
		ID: id, TenantID: 7, SandboxConfigID: configID, Name: name,
		Status: types.SkillStatusInstalling, Enabled: true,
	}
}

func TestSkillRepoIsolatesConfigs(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-2", "pdf")))

	list, err := repo.ListSkillsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "sk-a", list[0].ID)

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-b")
	require.NoError(t, err)
	require.Nil(t, got, "a skill from another config must read as absent, not as an error")
}

func TestSkillRepoUpdatePersistsPointerAndStatus(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))

	row, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	row.Status = types.SkillStatusReady
	row.InstalledSnapshotID = "snap-1"
	row.Enabled = false
	row.InstallSessionID = "sess-1"
	row.InstallMessageID = "msg-1"
	require.NoError(t, repo.UpdateSkill(ctx, row))

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Equal(t, "snap-1", got.InstalledSnapshotID)
	require.False(t, got.Enabled, "disabling a skill must round-trip; it is the visibility switch")
	// The locators go through the same explicit column map as everything else,
	// so a field added to the entity but not to that map reads back empty.
	require.Equal(t, "sess-1", got.InstallSessionID)
	require.Equal(t, "msg-1", got.InstallMessageID)
}

func TestSnapshotLedgerRecordsChain(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: "ins-1", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-a",
		ParentSnapshotID: "tpl-base", Generation: 1,
		Trigger: types.SkillSnapshotTriggerInstall,
		State:   types.SkillSnapshotStateBuilding,
	}))

	require.NoError(t, repo.MarkSnapshotState(ctx, 7, "ins-1", types.SkillSnapshotStateActive, "snap-1"))

	rows, err := repo.ListSnapshotsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.SkillSnapshotStateActive, rows[0].State)
	require.Equal(t, "snap-1", rows[0].SnapshotID)
}

func TestListStaleInstallingFindsAbandonedRuns(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-time.Hour)

	oldInstalling := skillRow("sk-old-install", "cfg-1", "old-install")
	sinceOld := cutoff.Add(-time.Hour)
	oldInstalling.InstallingSince = &sinceOld
	require.NoError(t, repo.CreateSkill(ctx, oldInstalling))

	oldRemoving := skillRow("sk-old-remove", "cfg-1", "old-remove")
	oldRemoving.Status = types.SkillStatusRemoving
	sinceRemove := cutoff.Add(-30 * time.Minute)
	oldRemoving.InstallingSince = &sinceRemove
	require.NoError(t, repo.CreateSkill(ctx, oldRemoving))

	fresh := skillRow("sk-new", "cfg-1", "new")
	now := time.Now()
	fresh.InstallingSince = &now
	require.NoError(t, repo.CreateSkill(ctx, fresh))

	ready := skillRow("sk-ready", "cfg-1", "ready")
	ready.Status = types.SkillStatusReady
	readySince := cutoff.Add(-2 * time.Hour)
	ready.InstallingSince = &readySince
	require.NoError(t, repo.CreateSkill(ctx, ready))

	atCutoff := skillRow("sk-at-cutoff", "cfg-1", "at-cutoff")
	atCutoff.InstallingSince = &cutoff
	require.NoError(t, repo.CreateSkill(ctx, atCutoff))

	stale, err := repo.ListStaleInstalling(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, stale, 2)
	ids := []string{stale[0].ID, stale[1].ID}
	require.ElementsMatch(t, []string{"sk-old-install", "sk-old-remove"}, ids)
}

func TestSkillRepoSoftDeleteAllowsNameReuse(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.DeleteSkill(ctx, 7, "cfg-1", "sk-a"))

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-1", "pdf")),
		"a soft-deleted name must be reusable for an in-place reinstall")
	got, err := repo.GetSkillByName(ctx, 7, "cfg-1", "pdf")
	require.NoError(t, err)
	require.Equal(t, "sk-b", got.ID)
}

func TestMarkSnapshotStateIsTenantScoped(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: "ins-1", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-a",
		ParentSnapshotID: "tpl-base", Generation: 1,
		Trigger: types.SkillSnapshotTriggerInstall,
		State:   types.SkillSnapshotStateBuilding,
	}))

	require.NoError(t, repo.MarkSnapshotState(ctx, 8, "ins-1", types.SkillSnapshotStateActive, "snap-stolen"))

	rows, err := repo.ListSnapshotsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.SkillSnapshotStateBuilding, rows[0].State,
		"a snapshot row must not move when the caller is a different tenant")
	require.Empty(t, rows[0].SnapshotID)
}

// skillEnvTestAESKey is exactly 32 bytes, the only length GetAESKey accepts.
const skillEnvTestAESKey = "0123456789abcdef0123456789abcdef"

func userEnvRow(p types.Principal, skillID, name, value string) *types.TenantUserEnvVar {
	return &types.TenantUserEnvVar{
		TenantID: 7, PrincipalType: p.Type, PrincipalID: p.ID,
		SandboxConfigID: "cfg-1", SkillID: skillID, Name: name, Value: value,
	}
}

func TestSkillRepoUpdateSkillEnvsPersistsTheDeclarationEncrypted(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo, db := newSkillTestRepoWithDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))

	require.NoError(t, repo.UpdateSkillEnvs(ctx, 7, "cfg-1", "sk-a", types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Description: "search key", Required: true, Value: "tvly-admin"},
		{Name: "OPTIONAL_KEY"},
	}))

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, got.Envs, 2)
	require.Equal(t, "TAVILY_API_KEY", got.Envs[0].Name)
	require.True(t, got.Envs[0].Required)
	require.Equal(t, "tvly-admin", got.Envs[0].Value)
	require.Empty(t, got.Envs[1].Value)

	var stored string
	require.NoError(t, db.Raw(`SELECT envs FROM tenant_skills WHERE id = ?`, "sk-a").Scan(&stored).Error)
	require.NotContains(t, stored, "tvly-admin", "the admin value must be encrypted at rest")
	require.Contains(t, stored, "TAVILY_API_KEY", "the declaration stays readable without a key")
}

// The install heartbeat rewrites the whole row from a copy it read seconds
// earlier. If UpdateSkill carried envs, that copy would undo a declaration or
// an admin value stored in between.
func TestSkillRepoUpdateSkillLeavesEnvsAlone(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo, _ := newSkillTestRepoWithDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))

	stale, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.NoError(t, repo.UpdateSkillEnvs(ctx, 7, "cfg-1", "sk-a", types.SkillEnvVars{
		{Name: "TAVILY_API_KEY", Required: true, Value: "tvly-admin"},
	}))

	stale.Status = types.SkillStatusInstalling
	require.NoError(t, repo.UpdateSkill(ctx, stale))

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusInstalling, got.Status)
	require.Len(t, got.Envs, 1)
	require.Equal(t, "tvly-admin", got.Envs[0].Value)
}

func TestSkillRepoUpdateSkillAdminStateWritesBothColumns(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo, _ := newSkillTestRepoWithDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))

	require.NoError(t, repo.UpdateSkillAdminState(ctx, 7, "cfg-1", "sk-a", false,
		types.SkillEnvVars{{Name: "TAVILY_API_KEY", Value: "tvly-admin"}}))

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Len(t, got.Envs, 1)
	require.Equal(t, "tvly-admin", got.Envs[0].Value)
}

func TestUpsertUserEnvIsIdempotentPerPrincipalSkillAndName(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo, db := newSkillTestRepoWithDB(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}

	first := userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "first")
	require.NoError(t, repo.UpsertUserEnvVar(ctx, first))
	require.Equal(t, "first", first.Value,
		"persisting an encrypted value must not replace the caller's plaintext")
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "second")))

	list, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, list, 1, "a second write to the same tuple replaces the value, it does not add a row")
	require.Equal(t, "second", list[0].Value)

	var stored string
	require.NoError(t, db.Raw(`SELECT value FROM tenant_user_env_vars`).Scan(&stored).Error)
	require.NotContains(t, stored, "second", "the user value must be encrypted at rest")
}

func TestUserEnvsAreIsolatedBetweenPrincipals(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}
	// Same ID, different type: the IM path's synthetic account is why the key
	// is the whole principal rather than a user id.
	imUser := types.Principal{Type: types.PrincipalIMUser, ID: "u-alice"}

	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "alice-key")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(imUser, "sk-a", "TAVILY_API_KEY", "im-key")))

	aliceEnvs, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, aliceEnvs, 1)
	require.Equal(t, "alice-key", aliceEnvs[0].Value)

	imEnvs, err := repo.ListUserEnvVars(ctx, 7, imUser, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, imEnvs, 1)
	require.Equal(t, "im-key", imEnvs[0].Value)
}

func TestDeleteSkillRemovesOnlyThatSkillsUserEnvs(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-1", "search")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "a")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-b", "TAVILY_API_KEY", "b")))

	require.NoError(t, repo.DeleteSkill(ctx, 7, "cfg-1", "sk-a"))

	// tenant_skills is soft-deleted, so a cascading foreign key would never
	// fire; the delete has to be explicit or the values outlive the skill.
	gone, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Empty(t, gone, "deleting a skill must take its user values with it")

	kept, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-b")
	require.NoError(t, err)
	require.Len(t, kept, 1, "another skill's values must survive")
	require.Equal(t, "b", kept[0].Value)
}

func TestDeleteSkillWithMismatchedConfigKeepsSkillAndUserEnvs(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "secret")))

	require.NoError(t, repo.DeleteSkill(ctx, 7, "cfg-stale", "sk-a"),
		"a missing scoped row remains an idempotent no-op")

	skill, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.NotNil(t, skill, "the mismatched config must not soft-delete the skill")

	kept, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, kept, 1,
		"user values must survive when the scoped soft delete affects no rows")
	require.Equal(t, "secret", kept[0].Value)
}

func TestDeleteUserEnvReportsAnAlreadyDeletedRow(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}

	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TAVILY_API_KEY", "a")))
	require.NoError(t, repo.DeleteUserEnvVar(ctx, 7, alice, "cfg-1", "sk-a", "TAVILY_API_KEY"))

	err := repo.DeleteUserEnvVar(ctx, 7, alice, "cfg-1", "sk-a", "TAVILY_API_KEY")
	require.ErrorIs(t, err, types.ErrEnvVarNotFound)
}

func TestListUserEnvVarsByConfigSpansScopesForOnePrincipal(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}
	bob := types.Principal{Type: types.PrincipalWebUser, ID: "u-bob"}

	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "ONE", "1")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "", "HTTP_PROXY", "2")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(bob, "sk-a", "ONE", "3")))

	list, err := repo.ListUserEnvVarsByConfig(ctx, 7, alice, "cfg-1")

	require.NoError(t, err)
	require.Len(t, list, 2, "both the config-wide and the skill-scoped value belong to alice")
	require.Equal(t, "", list[0].SkillID, "the config-wide row sorts first")
	require.Equal(t, "HTTP_PROXY", list[0].Name)
	require.Equal(t, "sk-a", list[1].SkillID)
}

// The same name in two scopes is two values: the config-wide one applies to
// every execution, the skill-scoped one only when a tool names that skill.
func TestUserEnvVarsKeepScopesApart(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}

	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "", "TOKEN", "config-wide")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TOKEN", "skill-scoped")))

	wide, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "")
	require.NoError(t, err)
	require.Len(t, wide, 1)
	require.Equal(t, "config-wide", wide[0].Value)

	scoped, err := repo.ListUserEnvVars(ctx, 7, alice, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Len(t, scoped, 1)
	require.Equal(t, "skill-scoped", scoped[0].Value)
}

// Deleting a config must take the config-wide rows too: DeleteSkill only ever
// sees rows filed under a skill.
func TestDeleteUserEnvVarsByConfigTakesEveryScopeAndPrincipal(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvTestAESKey)
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "u-alice"}
	bob := types.Principal{Type: types.PrincipalWebUser, ID: "u-bob"}

	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "", "HTTP_PROXY", "a")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(alice, "sk-a", "TOKEN", "b")))
	require.NoError(t, repo.UpsertUserEnvVar(ctx, userEnvRow(bob, "", "HTTP_PROXY", "c")))

	require.NoError(t, repo.DeleteUserEnvVarsByConfig(ctx, 7, "cfg-1"))

	aliceLeft, err := repo.ListUserEnvVarsByConfig(ctx, 7, alice, "cfg-1")
	require.NoError(t, err)
	require.Empty(t, aliceLeft)
	bobLeft, err := repo.ListUserEnvVarsByConfig(ctx, 7, bob, "cfg-1")
	require.NoError(t, err)
	require.Empty(t, bobLeft)
}

func TestListSkillsByTenantSpansConfigsAndStopsAtTheTenant(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-2", "search")))
	other := skillRow("sk-other", "cfg-3", "pdf")
	other.TenantID = 8
	require.NoError(t, repo.CreateSkill(ctx, other))

	list, err := repo.ListSkillsByTenant(ctx, 7)
	require.NoError(t, err)
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e.ID)
	}
	require.ElementsMatch(t, []string{"sk-a", "sk-b"}, ids,
		"every config of the tenant, and nothing from another tenant")
}

func TestCatalogNameIsUniquePerTenant(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-a", TenantID: 7, Name: "pdf",
	}))
	got, err := repo.GetCatalogByName(ctx, 7, "pdf")
	require.NoError(t, err)
	require.Equal(t, "cat-a", got.ID)

	err = repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-b", TenantID: 7, Name: "pdf",
	})
	require.Error(t, err, "same tenant cannot hold two catalog rows of one name")

	require.NoError(t, repo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-other", TenantID: 8, Name: "pdf",
	}))

	install := skillRow("sk-a", "cfg-1", "pdf")
	install.CatalogID = "cat-a"
	require.NoError(t, repo.CreateSkill(ctx, install))
	list, err := repo.ListSkillsByCatalog(ctx, 7, "cat-a")
	require.NoError(t, err)
	require.Len(t, list, 1)
}
