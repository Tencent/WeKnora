package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const wikiContributionStateVersion = 1

type wikiContributionStatePayload struct {
	Version  int               `json:"version"`
	Manifest map[string]string `json:"manifest"`
}

func wikiContributionStateKey(knowledgeBaseID, knowledgeID string) string {
	return processingCacheKey(
		"wiki-contribution-state-v1",
		strings.TrimSpace(knowledgeBaseID),
		strings.TrimSpace(knowledgeID),
	)
}

func wikiContributionFingerprint(update SlugUpdate) string {
	aliases := append([]string(nil), update.Item.Aliases...)
	sort.Strings(aliases)
	chunkIDs := append([]string(nil), update.SourceChunks...)
	sort.Strings(chunkIDs)
	return stableHash(
		"wiki-contribution-v1",
		update.Slug,
		update.Type,
		normalizeFingerprintText(update.DocTitle),
		update.KnowledgeID,
		update.SourceRef,
		update.Language,
		normalizeFingerprintText(update.SummaryLine),
		normalizeFingerprintText(update.SummaryBody),
		normalizeFingerprintText(update.DocSummary),
		normalizeFingerprintText(update.Item.Name),
		normalizeFingerprintText(update.Item.Description),
		normalizeFingerprintText(update.Item.Details),
		stableHash(aliases...),
		stableHash(chunkIDs...),
	)
}

func normalizeFingerprintText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func buildWikiContributionManifest(updates []SlugUpdate) map[string]string {
	bySlug := make(map[string][]string)
	for _, update := range updates {
		if update.Slug == "" || update.Type == "retract" || update.Type == "retractStale" {
			continue
		}
		bySlug[update.Slug] = append(bySlug[update.Slug], wikiContributionFingerprint(update))
	}
	manifest := make(map[string]string, len(bySlug))
	for slug, fingerprints := range bySlug {
		sort.Strings(fingerprints)
		manifest[slug] = stableHash(fingerprints...)
	}
	return manifest
}

func filterUnchangedWikiContributions(
	updates []SlugUpdate,
	oldManifest, newManifest map[string]string,
	oldStateFound bool,
) ([]SlugUpdate, []string) {
	if !oldStateFound {
		return updates, sortedManifestSlugs(newManifest, oldManifest)
	}
	changed := make(map[string]struct{})
	for slug, oldFingerprint := range oldManifest {
		if newManifest[slug] != oldFingerprint {
			changed[slug] = struct{}{}
		}
	}
	for slug, newFingerprint := range newManifest {
		if oldManifest[slug] != newFingerprint {
			changed[slug] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	filtered := make([]SlugUpdate, 0, len(updates))
	for _, update := range updates {
		if _, ok := changed[update.Slug]; ok {
			filtered = append(filtered, update)
		}
	}
	changedSlugs := make([]string, 0, len(changed))
	for slug := range changed {
		changedSlugs = append(changedSlugs, slug)
	}
	sort.Strings(changedSlugs)
	return filtered, changedSlugs
}

func sortedManifestSlugs(manifests ...map[string]string) []string {
	seen := make(map[string]struct{})
	for _, manifest := range manifests {
		for slug := range manifest {
			seen[slug] = struct{}{}
		}
	}
	slugs := make([]string, 0, len(seen))
	for slug := range seen {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

func (s *wikiIngestService) getWikiContributionState(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID, knowledgeID string,
) (map[string]string, bool) {
	if s.cacheRepo == nil {
		return nil, false
	}
	row, err := s.cacheRepo.Get(
		ctx, tenantID, types.ProcessingCacheStageWikiContribution,
		wikiContributionStateKey(knowledgeBaseID, knowledgeID),
	)
	if err != nil {
		logger.Warnf(ctx, "wiki contribution state lookup failed for %s: %v", knowledgeID, err)
		return nil, false
	}
	if row == nil {
		return nil, false
	}
	var payload wikiContributionStatePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil || payload.Version != wikiContributionStateVersion {
		logger.Warnf(ctx, "wiki contribution state invalid for %s: %v", knowledgeID, err)
		return nil, false
	}
	if payload.Manifest == nil {
		payload.Manifest = map[string]string{}
	}
	return payload.Manifest, true
}

func (s *wikiIngestService) putWikiContributionState(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID, knowledgeID string,
	manifest map[string]string,
) {
	if s.cacheRepo == nil {
		return
	}
	if manifest == nil {
		manifest = map[string]string{}
	}
	payloadBytes, err := json.Marshal(wikiContributionStatePayload{
		Version:  wikiContributionStateVersion,
		Manifest: manifest,
	})
	if err != nil {
		logger.Warnf(ctx, "wiki contribution state marshal failed for %s: %v", knowledgeID, err)
		return
	}
	metadata, _ := json.Marshal(map[string]any{
		"knowledge_base_id": knowledgeBaseID,
		"knowledge_id":      knowledgeID,
		"slugs":             len(manifest),
	})
	if err := s.cacheRepo.Upsert(ctx, &types.ProcessingCache{
		TenantID: tenantID,
		Stage:    types.ProcessingCacheStageWikiContribution,
		CacheKey: wikiContributionStateKey(knowledgeBaseID, knowledgeID),
		Payload:  types.JSON(payloadBytes),
		Metadata: types.JSON(metadata),
	}); err != nil {
		logger.Warnf(ctx, "wiki contribution state write failed for %s: %v", knowledgeID, err)
	}
}
