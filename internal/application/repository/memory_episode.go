package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// fallbackVectorScanCap bounds the in-process ranking fallback.
//
// One subject cannot hold more accounts than MemoryEpisodeMaxPerSubject, so
// this is a guard against a corrupted scope rather than a real limit: a
// subject at its cap still gets every one of its vectors scored, which is what
// makes the fallback a slower path rather than a worse one.
const fallbackVectorScanCap = 5000

// SaveEpisode files the account of one conversation, replacing the account
// already held for it.
//
// One account per conversation, rewritten as the conversation grows. Appending
// instead would give the digest two accounts of the same work in different
// words, and leave a reader no way to tell which one is current.
//
// Two things deliberately survive a rewrite. The usage counters, because an
// account the user keeps opening should not lose that history just because a
// later turn improved the text. And the slug, because it is the handle the
// digest's index points at: a pointer that changes every time the conversation
// continues is a pointer that is wrong more often than it is right.
func (r *memoryRepository) SaveEpisode(
	ctx context.Context, scope interfaces.MemoryScope, episode *types.MemoryEpisode,
) error {
	if episode == nil {
		return fmt.Errorf("memory: nil episode")
	}
	if episode.Slug == "" {
		return fmt.Errorf("memory: episode has no slug")
	}
	episode.TenantID, episode.SubjectID = scope.TenantID, scope.SubjectID
	now := time.Now()

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := lockEpisodeForSave(tx, scope, episode)
		if err != nil {
			return err
		}
		if existing == nil {
			slug, err := freeEpisodeSlug(tx, scope, episode.Slug, "")
			if err != nil {
				return err
			}
			episode.Slug = slug
			if episode.ID == "" {
				episode.ID = uuid.New().String()
			}
			episode.CreatedAt, episode.UpdatedAt = now, now
			return tx.Create(episode).Error
		}

		episode.ID = existing.ID
		episode.Slug = existing.Slug
		episode.UseCount = existing.UseCount
		episode.LastUsedAt = existing.LastUsedAt
		episode.CreatedAt = existing.CreatedAt
		episode.UpdatedAt = now
		// A rewritten account is new to the digest even though the row is not,
		// so the consolidation watermark goes back to zero and the next
		// rewrite reads it again.
		episode.DigestRevision = 0
		// The earliest turn covered only moves backwards: a later run that
		// read a shorter window must not narrow what the account claims to
		// cover.
		if !existing.FromAt.IsZero() && (episode.FromAt.IsZero() || existing.FromAt.Before(episode.FromAt)) {
			episode.FromAt = existing.FromAt
		}
		if err := tx.Model(existing).Select(
			"title", "outcome", "summary", "keywords",
			"from_at", "to_at", "digest_revision", "updated_at",
		).Updates(episode).Error; err != nil {
			return err
		}
		// The account changed, so its vector no longer describes it. Dropping
		// it leaves the episode reachable by keyword until the backfill
		// rebuilds it, which is better than reachable by a description of text
		// that no longer exists.
		return tx.Where("tenant_id = ? AND subject_id = ? AND episode_id = ?",
			scope.TenantID, scope.SubjectID, existing.ID).
			Delete(&types.MemoryEpisodeEmbedding{}).Error
	})
}

// lockEpisodeForSave finds the account this one replaces, if any: the
// conversation's own account, or an account already filed under this slug when
// there is no conversation to key on.
func lockEpisodeForSave(
	tx *gorm.DB, scope interfaces.MemoryScope, episode *types.MemoryEpisode,
) (*types.MemoryEpisode, error) {
	query := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
	if episode.SessionID != "" {
		query = query.Where("session_id = ?", episode.SessionID)
	} else {
		query = query.Where("slug = ?", episode.Slug)
	}
	var existing types.MemoryEpisode
	err := query.Clauses(forUpdateClause()).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// freeEpisodeSlug makes a proposed handle unique within the person's memory.
//
// Two conversations about the same thing get the same descriptive slug from the
// model, and the digest points at slugs — so one of them has to be
// distinguished. A numeric suffix keeps the handle readable, which a hash
// would not.
func freeEpisodeSlug(
	tx *gorm.DB, scope interfaces.MemoryScope, slug, exceptID string,
) (string, error) {
	for attempt := 0; attempt < 50; attempt++ {
		candidate := slug
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", slug, attempt+1)
		}
		query := tx.Model(&types.MemoryEpisode{}).
			Where("tenant_id = ? AND subject_id = ? AND slug = ?",
				scope.TenantID, scope.SubjectID, candidate)
		if exceptID != "" {
			query = query.Where("id <> ?", exceptID)
		}
		var taken int64
		if err := query.Count(&taken).Error; err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	// Fifty conversations under one descriptive slug means the slug says
	// nothing; fall back to something unique rather than failing the write.
	return fmt.Sprintf("%s-%s", slug, uuid.New().String()[:8]), nil
}

// GetEpisode returns one episode inside the scope, or (nil, nil) when absent.
func (r *memoryRepository) GetEpisode(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryEpisode, error) {
	var episode types.MemoryEpisode
	err := r.scoped(ctx, scope).Where("id = ?", id).First(&episode).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &episode, nil
}

// EpisodeBySession returns the account of one conversation, or (nil, nil)
// before the conversation has been distilled. It is what makes a rewrite a
// revision: the model is shown what it concluded last time rather than
// re-deciding the early turns from whatever still fits in the window.
func (r *memoryRepository) EpisodeBySession(
	ctx context.Context, scope interfaces.MemoryScope, sessionID string,
) (*types.MemoryEpisode, error) {
	if sessionID == "" {
		return nil, nil
	}
	var episode types.MemoryEpisode
	err := r.scoped(ctx, scope).Where("session_id = ?", sessionID).First(&episode).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &episode, nil
}

// EpisodeBySlug resolves a pointer from the digest's index.
func (r *memoryRepository) EpisodeBySlug(
	ctx context.Context, scope interfaces.MemoryScope, slug string,
) (*types.MemoryEpisode, error) {
	var episode types.MemoryEpisode
	err := r.scoped(ctx, scope).Where("slug = ?", slug).First(&episode).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &episode, nil
}

// ListEpisodes pages the memory manager, newest account first.
func (r *memoryRepository) ListEpisodes(
	ctx context.Context, scope interfaces.MemoryScope, limit, offset int,
) ([]*types.MemoryEpisode, int64, error) {
	query := r.scoped(ctx, scope).Model(&types.MemoryEpisode{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 20
	}
	var episodes []*types.MemoryEpisode
	// id breaks ties so paging stays deterministic: one run can file several
	// accounts in the same instant.
	err := query.Order("to_at DESC, id DESC").Limit(limit).Offset(offset).Find(&episodes).Error
	if err != nil {
		return nil, 0, err
	}
	return episodes, total, nil
}

// SelectEpisodesForDigest returns the accounts one consolidation should read.
//
// Ordering is by use, then by recency of use, then by recency of writing —
// Codex's phase-2 selection exactly. What it encodes is that the episodes worth
// keeping in the index are the ones something has actually needed; an account
// nobody has read in weeks is dropped from consideration even if it is recent,
// and a heavily used one stays regardless of age.
//
// An episode that has never been used falls back to when it was written, so a
// fresh account is not excluded for having had no chance to prove itself yet.
func (r *memoryRepository) SelectEpisodesForDigest(
	ctx context.Context, scope interfaces.MemoryScope, limit, unusedDays int,
) ([]*types.MemoryEpisode, error) {
	if limit <= 0 {
		limit = types.MemoryDigestMaxEpisodes
	}
	query := r.scoped(ctx, scope).Model(&types.MemoryEpisode{})
	if unusedDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -unusedDays)
		query = query.Where(
			"(last_used_at IS NOT NULL AND last_used_at > ?) OR (last_used_at IS NULL AND created_at > ?)",
			cutoff, cutoff,
		)
	}
	var episodes []*types.MemoryEpisode
	err := query.
		Order("use_count DESC, last_used_at DESC NULLS LAST, created_at DESC, id DESC").
		Limit(limit).Find(&episodes).Error
	if err != nil {
		return nil, err
	}
	return episodes, nil
}

// EpisodeKeywordCounts reports how often each retrieval handle appears across
// a person's accounts, most frequent first.
//
// This is what replaced the topic tracker, and it is worth being explicit about
// why a GROUP BY is a better answer than the machinery it retired. The old path
// asked a model to name the subject of each conversation, reduced that name to
// an identity key by deleting characters it guessed were uninformative, matched
// the key against existing subjects by character-bigram overlap, and asked a
// second model call to adjudicate whatever was left. Four chances to be wrong
// about whether two strings mean the same thing, and a wrong merge showed up
// only as a bumped counter on a subject the user never named.
//
// Keywords are already exact forms lifted from the accounts. Counting
// identical strings undercounts when the model varies its wording, and
// undercounting only delays — which is the trade the old code claimed to want,
// without it pretending to know that two different strings are one subject.
func (r *memoryRepository) EpisodeKeywordCounts(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]interfaces.MemoryKeywordCount, error) {
	if limit <= 0 {
		limit = 20
	}
	// jsonb_array_elements_text unnests the stored array. On a dialect without
	// it the counting happens in process below, which is the same answer for
	// the few hundred accounts one person can hold.
	if r.db != nil && r.db.Dialector != nil && r.db.Dialector.Name() == "postgres" {
		var rows []interfaces.MemoryKeywordCount
		err := r.db.WithContext(ctx).Raw(`
			SELECT keyword, COUNT(*) AS episodes
			FROM memory_episodes e,
			     jsonb_array_elements_text(e.keywords) AS keyword
			WHERE e.tenant_id = ? AND e.subject_id = ?
			  -- The column is nullable and holds model output. Unnesting
			  -- something that is not an array raises rather than returning
			  -- nothing, which would fail the whole query over one bad row.
			  AND jsonb_typeof(e.keywords) = 'array'
			GROUP BY keyword
			ORDER BY episodes DESC, keyword ASC
			LIMIT ?`, scope.TenantID, scope.SubjectID, limit).Scan(&rows).Error
		if err == nil {
			return rows, nil
		}
		logger.Warnf(ctx, "memory: keyword aggregation in SQL failed, counting in process: %v", err)
	}

	var episodes []*types.MemoryEpisode
	err := r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
		Select("keywords").Find(&episodes).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(episodes)*4)
	for _, episode := range episodes {
		// Per account, not per mention: a single conversation repeating a
		// keyword says nothing about recurrence, which is the whole signal.
		seen := make(map[string]struct{}, len(episode.Keywords))
		for _, keyword := range episode.Keywords {
			if _, duplicate := seen[keyword]; duplicate {
				continue
			}
			seen[keyword] = struct{}{}
			counts[keyword]++
		}
	}
	rows := make([]interfaces.MemoryKeywordCount, 0, len(counts))
	for keyword, episodes := range counts {
		rows = append(rows, interfaces.MemoryKeywordCount{Keyword: keyword, Episodes: episodes})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Episodes != rows[j].Episodes {
			return rows[i].Episodes > rows[j].Episodes
		}
		return rows[i].Keyword < rows[j].Keyword
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// CountEpisodesAwaitingDigest reports how many accounts the live digest has
// never seen. It is the dirty check that decides whether a consolidation call
// is worth making — the equivalent of Codex letting git decide whether its
// memory workspace changed.
//
// A zero watermark is the whole test, rather than a comparison against the
// live revision. SaveEpisode resets the watermark when it rewrites an account,
// so zero means "no profile has read this text", which is exactly the material
// a rewrite would be spending its call on. Comparing against the revision
// instead would count two kinds of account that are not new at all: the ones a
// rewrite just folded in, and the ones the selection cap leaves at an older
// revision forever.
func (r *memoryRepository) CountEpisodesAwaitingDigest(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	var count int64
	err := r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
		Where("digest_revision = 0").Count(&count).Error
	return count, err
}

// MarkEpisodesConsolidated records which accounts a successful rewrite read.
func (r *memoryRepository) MarkEpisodesConsolidated(
	ctx context.Context, scope interfaces.MemoryScope, ids []string, revision int64,
) error {
	if len(ids) == 0 {
		return nil
	}
	return r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{"digest_revision": revision, "updated_at": time.Now()}).Error
}

// TouchEpisodes records that these accounts were asked for.
//
// Asked for, not merely shown. It runs on the paths where something chose this
// account by name or by question — the search tool and the manager opening one
// — and use_count is what the digest's index and the cap are ranked by.
//
// Recall does not come through here, and that distinction is the point. An
// injected excerpt is this system guessing that an account is relevant, and
// counting the guess makes the ranking measure how often we guessed. Since
// injection is driven by similarity to the question, a guess that pays off
// feeds the next guess: an account matching a question the person keeps asking
// climbs, one that mattered once and decisively sinks, and the cap eventually
// deletes it. Codex draws the same line from the other end — it counts the
// memories a model cited, not the ones it was handed.
func (r *memoryRepository) TouchEpisodes(
	ctx context.Context, scope interfaces.MemoryScope, ids []string,
) error {
	return r.recordEpisodeRead(ctx, scope, ids, true)
}

// MarkEpisodesRecalled records that these accounts were relevant to a question
// without claiming anything read them.
//
// It moves last_used_at and leaves use_count alone, which keeps relevance
// doing the two jobs it can honestly do: holding an account inside the digest
// selection's unused-days window, and breaking ties among accounts nothing has
// ever asked for. What it cannot do is outrank an account somebody went
// looking for.
func (r *memoryRepository) MarkEpisodesRecalled(
	ctx context.Context, scope interfaces.MemoryScope, ids []string,
) error {
	return r.recordEpisodeRead(ctx, scope, ids, false)
}

func (r *memoryRepository) recordEpisodeRead(
	ctx context.Context, scope interfaces.MemoryScope, ids []string, requested bool,
) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	updates := map[string]interface{}{
		"last_used_at": now,
		"updated_at":   now,
	}
	if requested {
		updates["use_count"] = gorm.Expr("use_count + 1")
	}
	return r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
		Where("id IN ?", ids).Updates(updates).Error
}

// DeleteEpisode forgets one account. Forgetting means forgetting: the row goes,
// and the next consolidation rebuilds the digest from what is left, which is
// what removes the claims only this account supported.
func (r *memoryRepository) DeleteEpisode(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND subject_id = ? AND episode_id = ?",
			scope.TenantID, scope.SubjectID, id).
			Delete(&types.MemoryEpisodeEmbedding{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND subject_id = ? AND id = ?",
			scope.TenantID, scope.SubjectID, id).Delete(&types.MemoryEpisode{}).Error
	})
}

// DeleteAllEpisodes clears the scope and reports how many accounts went.
func (r *memoryRepository) DeleteAllEpisodes(
	ctx context.Context, scope interfaces.MemoryScope,
) (int64, error) {
	var removed int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Delete(&types.MemoryEpisodeEmbedding{}).Error; err != nil {
			return err
		}
		result := tx.Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
			Delete(&types.MemoryEpisode{})
		removed = result.RowsAffected
		return result.Error
	})
	return removed, err
}

// PruneEpisodes enforces the per-subject cap, dropping the least useful first.
//
// Least useful is least read, then longest unread, then oldest — the same order
// selection uses, reversed. Nothing is archived: an account nobody has opened
// in months, in a store already at its cap, is not being kept for anyone.
//
// An account no profile has read yet is exempt from the configured cap, and
// this is the important part. A freshly filed account has never been read, so
// on the plain ordering it sorts to the front of the queue for deletion: in a
// store where everything else has been recalled at least once, the account a
// model call just produced would be the one dropped, moments after it was
// written and before anything could consolidate it. Exempting the unread ones
// makes the cap fall on history that the profile already carries.
//
// The table ceiling is not exempt, or a deployment whose consolidation is
// broken would grow without bound while every account waits for a profile that
// never arrives.
func (r *memoryRepository) PruneEpisodes(
	ctx context.Context, scope interfaces.MemoryScope, keep int,
) (int64, error) {
	if keep <= 0 {
		keep = types.MemoryEpisodeMaxPerSubject
	}
	var count int64
	if err := r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).Count(&count).Error; err != nil {
		return 0, err
	}
	if count <= int64(keep) {
		return 0, nil
	}

	const leastUseful = "use_count ASC, last_used_at ASC NULLS FIRST, created_at ASC, id ASC"

	var doomed []string
	err := r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
		Where("digest_revision > 0").
		Order(leastUseful).
		Limit(int(count-int64(keep))).Pluck("id", &doomed).Error
	if err != nil {
		return 0, err
	}
	// Whatever the consolidated accounts could not cover, taken from the rest
	// only as far as the table ceiling demands.
	if over := count - int64(len(doomed)) - int64(types.MemoryEpisodeMaxPerSubject); over > 0 {
		var overflow []string
		err := r.scoped(ctx, scope).Model(&types.MemoryEpisode{}).
			Where("digest_revision = 0").
			Order(leastUseful).
			Limit(int(over)).Pluck("id", &overflow).Error
		if err != nil {
			return 0, err
		}
		doomed = append(doomed, overflow...)
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	var removed int64
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND subject_id = ? AND episode_id IN ?",
			scope.TenantID, scope.SubjectID, doomed).
			Delete(&types.MemoryEpisodeEmbedding{}).Error; err != nil {
			return err
		}
		result := tx.Where("tenant_id = ? AND subject_id = ? AND id IN ?",
			scope.TenantID, scope.SubjectID, doomed).Delete(&types.MemoryEpisode{})
		removed = result.RowsAffected
		return result.Error
	})
	return removed, err
}

// ---------------------------------------------------------------------------
// Episode vectors
// ---------------------------------------------------------------------------

// episodeVectorColumnReady reports whether the database can rank episode
// vectors itself.
//
// Checked once and cached. It is false on SQLite, and false on a PostgreSQL
// deployment that never installed pgvector because its retrieval driver does
// not use it — both of which keep working through the in-process fallback,
// just with every vector crossing the wire.
func (r *memoryRepository) episodeVectorColumnReady() bool {
	r.episodeVectorOnce.Do(func() {
		if r.db == nil || r.db.Dialector == nil || r.db.Dialector.Name() != "postgres" {
			return
		}
		r.episodeVectorColumn = r.db.Migrator().HasColumn(&types.MemoryEpisodeEmbedding{}, "embedding")
	})
	return r.episodeVectorColumn
}

// UpsertEpisodeEmbedding stores or replaces the vector for one account.
func (r *memoryRepository) UpsertEpisodeEmbedding(
	ctx context.Context, scope interfaces.MemoryScope, embedding *types.MemoryEpisodeEmbedding,
) error {
	if embedding == nil || embedding.EpisodeID == "" {
		return nil
	}
	embedding.TenantID, embedding.SubjectID = scope.TenantID, scope.SubjectID
	now := time.Now()
	embedding.CreatedAt, embedding.UpdatedAt = now, now

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The episode has to still exist and still be the one that was
		// embedded: an account rewritten while the embedding call was in
		// flight would otherwise be described by the vector of a version that
		// no longer exists.
		var episode types.MemoryEpisode
		err := tx.Where("tenant_id = ? AND subject_id = ? AND id = ?",
			scope.TenantID, scope.SubjectID, embedding.EpisodeID).First(&episode).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "episode_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"model_id", "dims", "vector", "updated_at"}),
		}).Create(embedding).Error; err != nil {
			return err
		}
		return r.writeEpisodeVectorColumn(tx, embedding.EpisodeID, embedding.Vector)
	})
}

// writeEpisodeVectorColumn keeps the database's own vector type in step with
// the stored blob. Best effort inside the caller's transaction: the blob is the
// source of truth, and a row whose vector column is behind is picked up by the
// sync pass rather than lost.
func (r *memoryRepository) writeEpisodeVectorColumn(tx *gorm.DB, episodeID string, raw []byte) error {
	if !r.episodeVectorColumnReady() || episodeID == "" {
		return nil
	}
	literal := types.FormatEmbeddingLiteral(types.DecodeEmbedding(raw))
	if literal == "" {
		return nil
	}
	return tx.Exec(
		`UPDATE memory_episode_embeddings SET embedding = ?::halfvec WHERE episode_id = ?`,
		literal, episodeID,
	).Error
}

// EpisodesMissingEmbeddings returns accounts with no usable vector yet, so a
// background pass can fill them in.
func (r *memoryRepository) EpisodesMissingEmbeddings(
	ctx context.Context, scope interfaces.MemoryScope, modelID string, limit int,
) ([]*types.MemoryEpisode, error) {
	if limit <= 0 {
		limit = 20
	}
	var episodes []*types.MemoryEpisode
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT e.* FROM memory_episodes e
			LEFT JOIN memory_episode_embeddings v
			  ON v.episode_id = e.id AND v.model_id = ?
			WHERE e.tenant_id = ? AND e.subject_id = ? AND v.episode_id IS NULL
			ORDER BY e.created_at DESC
			LIMIT ?`, modelID, scope.TenantID, scope.SubjectID, limit).
		Scan(&episodes).Error
	if err != nil {
		return nil, err
	}
	return episodes, nil
}

// SearchEpisodesByVector ranks this subject's accounts against one query.
//
// Every vector the subject has is a candidate. This is the property the old
// item search had to be rewritten to get and the one that matters most here:
// the account that answers a question is not especially likely to be a recent
// or a heavily used one, so any pre-selection puts a ceiling on what memory can
// find.
func (r *memoryRepository) SearchEpisodesByVector(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery,
) ([]interfaces.MemoryEpisodeHit, error) {
	if !scope.Valid() || query.ModelID == "" || len(query.Vector) == 0 {
		return nil, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}

	var rows []episodeHitRow
	var err error
	if r.episodeVectorColumnReady() {
		rows, err = r.rankEpisodesInDatabase(ctx, scope, query, limit)
	} else {
		rows, err = r.rankEpisodesInProcess(ctx, scope, query, limit)
	}
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Score >= query.MinScore {
			ids = append(ids, row.EpisodeID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	var episodes []*types.MemoryEpisode
	if err := r.scoped(ctx, scope).Where("id IN ?", ids).Find(&episodes).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]*types.MemoryEpisode, len(episodes))
	for _, episode := range episodes {
		byID[episode.ID] = episode
	}

	hits := make([]interfaces.MemoryEpisodeHit, 0, len(ids))
	for _, row := range rows {
		episode, ok := byID[row.EpisodeID]
		if !ok {
			continue
		}
		hits = append(hits, interfaces.MemoryEpisodeHit{Episode: episode, Score: row.Score})
	}
	return hits, nil
}

type episodeHitRow struct {
	EpisodeID string
	Score     float64
}

func (r *memoryRepository) rankEpisodesInDatabase(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery, limit int,
) ([]episodeHitRow, error) {
	literal := types.FormatEmbeddingLiteral(query.Vector)
	if literal == "" {
		return nil, nil
	}
	var rows []episodeHitRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT v.episode_id AS episode_id,
		       1 - (v.embedding::halfvec <=> ?::halfvec) AS score
		FROM memory_episode_embeddings v
		JOIN memory_episodes e
		  ON e.id = v.episode_id AND e.tenant_id = v.tenant_id AND e.subject_id = v.subject_id
		WHERE v.tenant_id = ? AND v.subject_id = ?
		  AND v.model_id = ? AND v.dims = ? AND v.embedding IS NOT NULL
		ORDER BY v.embedding::halfvec <=> ?::halfvec
		LIMIT ?`,
		literal, scope.TenantID, scope.SubjectID,
		query.ModelID, len(query.Vector), literal, limit,
	).Scan(&rows).Error
	return rows, err
}

func (r *memoryRepository) rankEpisodesInProcess(
	ctx context.Context, scope interfaces.MemoryScope, query interfaces.MemoryVectorQuery, limit int,
) ([]episodeHitRow, error) {
	type storedVector struct {
		EpisodeID string
		Vector    []byte
	}
	var stored []storedVector
	err := r.db.WithContext(ctx).Raw(`
		SELECT v.episode_id AS episode_id, v.vector AS vector
		FROM memory_episode_embeddings v
		JOIN memory_episodes e
		  ON e.id = v.episode_id AND e.tenant_id = v.tenant_id AND e.subject_id = v.subject_id
		WHERE v.tenant_id = ? AND v.subject_id = ?
		  AND v.model_id = ? AND v.dims = ?
		LIMIT ?`,
		scope.TenantID, scope.SubjectID, query.ModelID, len(query.Vector), fallbackVectorScanCap,
	).Scan(&stored).Error
	if err != nil {
		return nil, err
	}
	rows := make([]episodeHitRow, 0, len(stored))
	for _, row := range stored {
		vector := types.DecodeEmbedding(row.Vector)
		if len(vector) == 0 {
			continue
		}
		rows = append(rows, episodeHitRow{
			EpisodeID: row.EpisodeID,
			Score:     types.CosineSimilarity(query.Vector, vector),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Score > rows[j].Score })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// SyncEpisodeVectorColumn moves already-embedded accounts into the database's
// vector type, for rows written before the column existed. No model calls: the
// numbers are already stored, only their representation is behind.
func (r *memoryRepository) SyncEpisodeVectorColumn(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) (int, error) {
	if !r.episodeVectorColumnReady() || !scope.Valid() {
		return 0, nil
	}
	if limit <= 0 {
		limit = 200
	}
	type pending struct {
		EpisodeID string
		Vector    []byte
	}
	var rows []pending
	err := r.db.WithContext(ctx).Raw(`
		SELECT episode_id, vector FROM memory_episode_embeddings
		WHERE tenant_id = ? AND subject_id = ?
		  AND embedding IS NULL AND vector IS NOT NULL
		LIMIT ?`, scope.TenantID, scope.SubjectID, limit).Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, row := range rows {
		if err := r.writeEpisodeVectorColumn(r.db.WithContext(ctx), row.EpisodeID, row.Vector); err != nil {
			logger.Warnf(ctx, "memory: sync episode vector failed for %s: %v", row.EpisodeID, err)
			continue
		}
		moved++
	}
	return moved, nil
}
