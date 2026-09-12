package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEvaluationDatasetFixture(tenantID uint64, suffix string) *types.EvaluationDataset {
	createdAt := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	return &types.EvaluationDataset{
		ID:            "dataset-" + suffix,
		Scope:         types.EvaluationDatasetScopeTenant,
		OwnerTenantID: &tenantID,
		Name:          "Dataset " + suffix,
		Description:   "fixture",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
}

func newEvaluationDatasetVersionFixture(
	datasetID string,
	suffix string,
) (*types.EvaluationDatasetVersion, *types.EvaluationDatasetVersionInput) {
	content := &types.EvaluationDatasetVersionInput{
		Passages: []types.EvaluationDatasetPassageInput{
			{PID: "p1", Content: "passage one"},
			{PID: "p2", Content: "passage two", Metadata: types.JSON(`{"lang":"zh"}`)},
			{PID: "p3", Content: "passage three"},
		},
		Questions: []types.EvaluationDatasetQuestionInput{
			{QID: "q1", Question: "question one?", Answer: "answer one"},
			{QID: "q2", Question: "question two?", Answer: "answer two"},
		},
		Relevance: []types.EvaluationDatasetRelevanceInput{
			{QID: "q1", PID: "p1", Grade: 2},
			{QID: "q1", PID: "p2", Grade: 1},
			{QID: "q2", PID: "p3", Grade: 1},
		},
	}
	version := &types.EvaluationDatasetVersion{
		ID:             "dataset-version-" + suffix,
		DatasetID:      datasetID,
		VersionNumber:  1,
		SchemaVersion:  types.EvaluationDatasetSchemaVersion,
		ArtifactSHA256: types.EvaluationDatasetArtifactSHA256([]byte("artifact-" + suffix)),
		ContentSHA256:  types.CanonicalEvaluationDatasetContentSHA256(content),
		Manifest:       types.JSON(`{"source":"test"}`),
		PassageCount:   len(content.Passages),
		QuestionCount:  len(content.Questions),
		RelevanceCount: len(content.Relevance),
		CreatedAt:      time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC),
	}
	return version, content
}

func TestEvaluationDatasetRepositoryTenantIsolation(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()

	systemDataset := &types.EvaluationDataset{
		ID:    "dataset-system",
		Scope: types.EvaluationDatasetScopeSystem,
		Name:  "Built-in samples",
	}
	require.NoError(t, repo.CreateDataset(ctx, systemDataset))
	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "tenant-7")))

	// System datasets are readable by any tenant.
	got, err := repo.GetDataset(ctx, 99, "dataset-system")
	require.NoError(t, err)
	assert.Nil(t, got.OwnerTenantID)

	// Tenant datasets are isolated: cross-tenant access is the same NotFound
	// as a missing dataset, so existence is not leaked.
	got, err = repo.GetDataset(ctx, 7, "dataset-tenant-7")
	require.NoError(t, err)
	require.NotNil(t, got.OwnerTenantID)
	assert.Equal(t, uint64(7), *got.OwnerTenantID)

	_, err = repo.GetDataset(ctx, 8, "dataset-tenant-7")
	require.ErrorIs(t, err, ErrEvaluationDatasetNotFound)
	_, err = repo.GetDataset(ctx, 7, "dataset-missing")
	require.ErrorIs(t, err, ErrEvaluationDatasetNotFound)

	visible7, err := repo.ListDatasets(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, visible7, 2)
	visible8, err := repo.ListDatasets(ctx, 8)
	require.NoError(t, err)
	assert.Len(t, visible8, 1)
	assert.Equal(t, "dataset-system", visible8[0].ID)
}

func TestEvaluationDatasetRepositoryRejectsInvalidDataset(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()

	tenant := uint64(7)
	tests := []struct {
		name    string
		dataset *types.EvaluationDataset
	}{
		{name: "nil"},
		{name: "missing id", dataset: &types.EvaluationDataset{
			Scope:         types.EvaluationDatasetScopeTenant,
			OwnerTenantID: &tenant, Name: "x",
		}},
		{name: "system with owner", dataset: &types.EvaluationDataset{
			ID:    "d1",
			Scope: types.EvaluationDatasetScopeSystem, OwnerTenantID: &tenant, Name: "x",
		}},
		{name: "tenant without owner", dataset: &types.EvaluationDataset{
			ID:    "d2",
			Scope: types.EvaluationDatasetScopeTenant, Name: "x",
		}},
		{name: "unknown scope", dataset: &types.EvaluationDataset{
			ID:    "d3",
			Scope: "public", Name: "x",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, repo.CreateDataset(ctx, tc.dataset))
		})
	}

	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "dup")))
	err := repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "dup"))
	require.ErrorIs(t, err, ErrEvaluationDatasetAlreadyExists)
}

func TestEvaluationDatasetRepositoryCreatesImmutableVersionAtomically(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "content")))
	version, content := newEvaluationDatasetVersionFixture("dataset-content", "v1")
	require.NoError(t, repo.CreateVersion(ctx, version, content))

	got, err := repo.GetVersion(ctx, 7, version.ID)
	require.NoError(t, err)
	assert.Equal(t, version.ContentSHA256, got.ContentSHA256)
	assert.Equal(t, 3, got.PassageCount)

	// Full content round-trips in canonical order with identical hashes.
	full, err := repo.GetVersionContent(ctx, 7, version.ID)
	require.NoError(t, err)
	require.Len(t, full.Questions, 2)
	assert.Equal(t, 0, full.Questions[0].SampleIndex)
	assert.Equal(t, "q1", full.Questions[0].QID)
	assert.Equal(t, 1, full.Questions[1].SampleIndex)
	require.Len(t, full.Relevance, 3)
	assert.Equal(t, 2, full.Relevance[0].Grade)

	recomputed := types.CanonicalEvaluationDatasetContentSHA256(&types.EvaluationDatasetVersionInput{
		Passages: []types.EvaluationDatasetPassageInput{
			{PID: full.Passages[0].PID, Content: full.Passages[0].Content, Metadata: full.Passages[0].Metadata},
			{PID: full.Passages[1].PID, Content: full.Passages[1].Content, Metadata: full.Passages[1].Metadata},
			{PID: full.Passages[2].PID, Content: full.Passages[2].Content, Metadata: full.Passages[2].Metadata},
		},
		Questions: []types.EvaluationDatasetQuestionInput{
			{QID: "q1", Question: "question one?", Answer: "answer one"},
			{QID: "q2", Question: "question two?", Answer: "answer two"},
		},
		Relevance: []types.EvaluationDatasetRelevanceInput{
			{QID: "q1", PID: "p1", Grade: 2},
			{QID: "q1", PID: "p2", Grade: 1},
			{QID: "q2", PID: "p3", Grade: 1},
		},
	})
	assert.Equal(t, version.ContentSHA256, recomputed)

	// Dataset now points at the new current version.
	dataset, err := repo.GetDataset(ctx, 7, "dataset-content")
	require.NoError(t, err)
	assert.Equal(t, version.ID, dataset.CurrentVersionID)

	versions, err := repo.ListVersions(ctx, 7, "dataset-content")
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, version.ID, versions[0].ID)
}

func TestEvaluationDatasetRepositoryRejectsInvalidVersionAtomically(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "atomic")))

	cases := []struct {
		name   string
		mutate func(*types.EvaluationDatasetVersion, *types.EvaluationDatasetVersionInput)
	}{
		{"unknown dataset", func(v *types.EvaluationDatasetVersion, _ *types.EvaluationDatasetVersionInput) {
			v.DatasetID = "dataset-missing"
		}},
		{"unknown relevance question", func(_ *types.EvaluationDatasetVersion, c *types.EvaluationDatasetVersionInput) {
			c.Relevance[0].QID = "q-missing"
		}},
		{"unknown relevance passage", func(_ *types.EvaluationDatasetVersion, c *types.EvaluationDatasetVersionInput) {
			c.Relevance[0].PID = "p-missing"
		}},
		{"duplicate passage id", func(_ *types.EvaluationDatasetVersion, c *types.EvaluationDatasetVersionInput) {
			c.Passages = append(c.Passages, c.Passages[0])
		}},
		{"duplicate question id", func(_ *types.EvaluationDatasetVersion, c *types.EvaluationDatasetVersionInput) {
			c.Questions = append(c.Questions, c.Questions[0])
		}},
		{"duplicate relevance edge", func(_ *types.EvaluationDatasetVersion, c *types.EvaluationDatasetVersionInput) {
			c.Relevance = append(c.Relevance, c.Relevance[0])
		}},
		{"count mismatch", func(v *types.EvaluationDatasetVersion, _ *types.EvaluationDatasetVersionInput) {
			v.QuestionCount = 99
		}},
		{"short content hash", func(v *types.EvaluationDatasetVersion, _ *types.EvaluationDatasetVersionInput) {
			v.ContentSHA256 = "abcd"
		}},
		{"wrong schema version", func(v *types.EvaluationDatasetVersion, _ *types.EvaluationDatasetVersionInput) {
			v.SchemaVersion = 2
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			version, content := newEvaluationDatasetVersionFixture("dataset-atomic", "atomic-"+tc.name)
			tc.mutate(version, content)
			require.Error(t, repo.CreateVersion(ctx, version, content))

			// Atomic failure: nothing of this version remains.
			_, err := repo.GetVersion(ctx, 7, version.ID)
			require.ErrorIs(t, err, ErrEvaluationDatasetVersionNotFound)
		})
	}

	var versionCount, passageCount, questionCount, relevanceCount int64
	require.NoError(t, db.Model(&types.EvaluationDatasetVersion{}).Count(&versionCount).Error)
	require.NoError(t, db.Model(&types.EvaluationDatasetPassage{}).Count(&passageCount).Error)
	require.NoError(t, db.Model(&types.EvaluationDatasetQuestion{}).Count(&questionCount).Error)
	require.NoError(t, db.Model(&types.EvaluationDatasetRelevance{}).Count(&relevanceCount).Error)
	assert.Zero(t, versionCount)
	assert.Zero(t, passageCount)
	assert.Zero(t, questionCount)
	assert.Zero(t, relevanceCount)
}

func TestEvaluationDatasetRepositoryRejectsConflictingVersion(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "conflict")))

	version, content := newEvaluationDatasetVersionFixture("dataset-conflict", "first")
	require.NoError(t, repo.CreateVersion(ctx, version, content))

	// Same version_number with different content conflicts.
	duplicateNumber, duplicateContent := newEvaluationDatasetVersionFixture("dataset-conflict", "second")
	duplicateContent.Questions[0].Answer = "changed answer"
	duplicateNumber.ContentSHA256 = types.CanonicalEvaluationDatasetContentSHA256(duplicateContent)
	err := repo.CreateVersion(ctx, duplicateNumber, duplicateContent)
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionConflict)

	// Same canonical content under a new version_number also conflicts:
	// one logical content has exactly one immutable version per dataset.
	duplicateHash, sameContent := newEvaluationDatasetVersionFixture("dataset-conflict", "third")
	duplicateHash.VersionNumber = 2
	err = repo.CreateVersion(ctx, duplicateHash, sameContent)
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionConflict)

	versions, err := repo.ListVersions(ctx, 7, "dataset-conflict")
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

func TestEvaluationDatasetRepositoryVersionVisibilityFollowsDataset(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationDatasetRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateDataset(ctx, newEvaluationDatasetFixture(7, "hidden")))
	version, content := newEvaluationDatasetVersionFixture("dataset-hidden", "hidden-v1")
	require.NoError(t, repo.CreateVersion(ctx, version, content))

	_, err := repo.GetVersion(ctx, 8, version.ID)
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionNotFound)
	_, err = repo.GetVersionContent(ctx, 8, version.ID)
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionNotFound)
	_, err = repo.ListVersions(ctx, 8, "dataset-hidden")
	require.ErrorIs(t, err, ErrEvaluationDatasetNotFound)
	_, err = repo.GetVersion(ctx, 7, "dataset-version-missing")
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionNotFound)
}
