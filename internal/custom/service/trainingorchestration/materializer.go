package trainingorchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

// EvidenceReader is the narrow stage-four seam for reading only evidence
// explicitly requested by an accepted plan.
type EvidenceReader interface {
	ReadEvidence(context.Context, string, string, []string) ([]transcript.Chunk, error)
}

// Materializer turns an accepted plan into one independently validated
// material package per topic cluster. It never reads material for abstained or
// rejected clusters.
type Materializer struct {
	Wiki            WikiReader
	Evidence        EvidenceReader
	KnowledgeBaseID string
}

func (m *Materializer) Materialize(ctx context.Context, snapshot CatalogSnapshot, plan PlanDraft) ([]ClusterMaterial, error) {
	if m == nil || m.Wiki == nil || m.Evidence == nil {
		return nil, fmt.Errorf("training orchestration materializer dependencies are not configured")
	}
	if strings.TrimSpace(m.KnowledgeBaseID) == "" {
		return nil, fmt.Errorf("training orchestration materializer knowledge base is required")
	}
	if err := plan.ValidateAgainst(snapshot); err != nil {
		return nil, fmt.Errorf("validate training orchestration plan: %w", err)
	}
	knowledgeByVideo, err := m.readAuditedKnowledge(ctx, snapshot)
	if err != nil {
		return nil, err
	}

	videoByID := make(map[string]CatalogVideo, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		videoByID[strings.TrimSpace(video.VideoID)] = video
	}
	materials := make([]ClusterMaterial, 0, len(plan.TopicClusters))
	for clusterIndex, cluster := range plan.TopicClusters {
		if cluster.ReviewStatus != PlanAccepted {
			continue
		}
		material := ClusterMaterial{
			ContractVersion:  MaterialContractVersion,
			ClusterKey:       cluster.ClusterKey,
			SourceVideoIDs:   append([]string(nil), cluster.SourceVideoIDs...),
			SummaryBlocks:    []MaterialBlock{},
			Evidence:         []MaterialEvidence{},
			KnowledgeObjects: []MaterialKnowledge{},
		}
		for requestIndex, request := range cluster.MaterialRequests {
			video, ok := videoByID[strings.TrimSpace(request.VideoID)]
			if !ok {
				return nil, fmt.Errorf("materialize cluster %d request %d: video %s is outside catalog", clusterIndex+1, requestIndex+1, request.VideoID)
			}
			if strings.TrimSpace(request.SummaryWikiPageID) != "" {
				blocks, err := m.readSummaryBlocks(ctx, video, request)
				if err != nil {
					return nil, fmt.Errorf("materialize cluster %d request %d summary: %w", clusterIndex+1, requestIndex+1, err)
				}
				material.SummaryBlocks = append(material.SummaryBlocks, blocks...)
			} else if len(request.SummaryBlockIDs) > 0 {
				return nil, fmt.Errorf("materialize cluster %d request %d has summary block IDs without a summary page", clusterIndex+1, requestIndex+1)
			}

			chunks, err := m.Evidence.ReadEvidence(ctx, video.VideoID, video.TranscriptGeneration, request.EvidenceIDs)
			if err != nil {
				return nil, fmt.Errorf("materialize cluster %d request %d evidence: %w", clusterIndex+1, requestIndex+1, err)
			}
			chunksByEvidenceID := make(map[string]transcript.Chunk, len(chunks))
			for _, chunk := range chunks {
				id := strings.TrimSpace(chunk.EvidenceSentenceID)
				if id == "" {
					return nil, fmt.Errorf("materialize cluster %d request %d returned evidence without immutable ID", clusterIndex+1, requestIndex+1)
				}
				if _, duplicate := chunksByEvidenceID[id]; duplicate {
					return nil, fmt.Errorf("materialize cluster %d request %d returned duplicate evidence %s", clusterIndex+1, requestIndex+1, id)
				}
				chunksByEvidenceID[id] = chunk
			}
			for _, evidenceID := range request.EvidenceIDs {
				chunk, ok := chunksByEvidenceID[strings.TrimSpace(evidenceID)]
				if !ok {
					return nil, fmt.Errorf("materialize cluster %d request %d missing evidence %s", clusterIndex+1, requestIndex+1, evidenceID)
				}
				material.Evidence = append(material.Evidence, MaterialEvidence{
					VideoID:              video.VideoID,
					TranscriptGeneration: video.TranscriptGeneration,
					EvidenceID:           chunk.EvidenceSentenceID,
					StartMs:              chunk.StartMs,
					EndMs:                chunk.EndMs,
					Text:                 strings.TrimSpace(transcript.OriginalText(chunk.Content)),
				})
			}
		}
		for _, videoID := range cluster.SourceVideoIDs {
			material.KnowledgeObjects = append(material.KnowledgeObjects, knowledgeByVideo[strings.TrimSpace(videoID)]...)
		}
		if err := material.ValidateAgainst(cluster); err != nil {
			return nil, fmt.Errorf("validate material for cluster %s: %w", cluster.ClusterKey, err)
		}
		materials = append(materials, material)
	}
	return materials, nil
}

func (m *Materializer) readAuditedKnowledge(ctx context.Context, snapshot CatalogSnapshot) (map[string][]MaterialKnowledge, error) {
	reader, ok := m.Wiki.(KnowledgePageReader)
	if !ok {
		return map[string][]MaterialKnowledge{}, nil
	}
	pages, err := reader.ListAllPages(ctx, m.KnowledgeBaseID, "")
	if err != nil {
		return nil, fmt.Errorf("list audited knowledge objects: %w", err)
	}
	result := make(map[string][]MaterialKnowledge)
	for _, video := range snapshot.Videos {
		videoID := strings.TrimSpace(video.VideoID)
		generation := strings.TrimSpace(video.TranscriptGeneration)
		for _, page := range pages {
			if page.PageType != "index" || strings.TrimSpace(page.ID) == "" {
				continue
			}
			validation, validationErr := knowledge.ValidateWikiObjectPage(page.Content, page.PageType, videoID, generation)
			if validationErr != nil {
				continue
			}
			evidenceIDs := append([]string(nil), validation.EvidenceIDs...)
			for _, contribution := range validation.EvidenceContributions {
				if contribution.VideoID == videoID && contribution.TranscriptGeneration == generation && strings.EqualFold(strings.TrimSpace(contribution.QualityStatus), "passed") {
					evidenceIDs = append([]string(nil), contribution.EvidenceIDs...)
					break
				}
			}
			result[videoID] = append(result[videoID], MaterialKnowledge{
				VideoID: videoID, KnowledgeObjectID: validation.KnowledgeObjectID,
				WikiPageID: page.ID, KnowledgeType: validation.KnowledgeType, Title: validation.Title,
				EvidenceIDs: uniqueStrings(evidenceIDs),
			})
		}
		result[videoID] = dedupeMaterialKnowledge(result[videoID])
	}
	return result, nil
}

func dedupeMaterialKnowledge(values []MaterialKnowledge) []MaterialKnowledge {
	seen := make(map[string]struct{}, len(values))
	result := make([]MaterialKnowledge, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value.VideoID) + "\x00" + strings.TrimSpace(value.KnowledgeObjectID) + "\x00" + strings.TrimSpace(value.WikiPageID)
		if key == "\x00\x00\x00" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		value.EvidenceIDs = uniqueStrings(value.EvidenceIDs)
		result = append(result, value)
	}
	return result
}

func (m *Materializer) readSummaryBlocks(ctx context.Context, video CatalogVideo, request MaterialRequest) ([]MaterialBlock, error) {
	page, err := m.Wiki.GetPage(ctx, m.KnowledgeBaseID, "typed-summary/"+video.VideoID)
	if err != nil {
		return nil, fmt.Errorf("read typed summary: %w", err)
	}
	if page == nil {
		return nil, fmt.Errorf("typed summary does not exist")
	}
	if strings.TrimSpace(page.ID) != strings.TrimSpace(request.SummaryWikiPageID) ||
		strings.TrimSpace(page.Slug) != "typed-summary/"+strings.TrimSpace(video.VideoID) ||
		page.Version != request.SummaryVersion {
		return nil, fmt.Errorf("typed summary identity or version changed")
	}
	frontmatter := page.ParsedFrontmatter()
	if !strings.EqualFold(frontmatterString(frontmatter, "type"), "typed_summary") ||
		frontmatterString(frontmatter, "source_video_id") != strings.TrimSpace(video.VideoID) ||
		frontmatterString(frontmatter, "transcript_generation") != strings.TrimSpace(video.TranscriptGeneration) {
		return nil, fmt.Errorf("typed summary source identity changed")
	}
	document, err := summary.ParseStored(page.Content)
	if err != nil {
		return nil, fmt.Errorf("parse typed summary: %w", err)
	}
	if err := summary.ValidateStored(document, video.VideoType); err != nil {
		return nil, fmt.Errorf("validate typed summary: %w", err)
	}
	blockByID := make(map[string]summary.Block)
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			blockByID[strings.TrimSpace(block.ID)] = block
		}
	}
	blocks := make([]MaterialBlock, 0, len(request.SummaryBlockIDs))
	for _, blockID := range request.SummaryBlockIDs {
		block, ok := blockByID[strings.TrimSpace(blockID)]
		if !ok || strings.TrimSpace(block.Text) == "" {
			return nil, fmt.Errorf("requested summary block %s is unavailable", blockID)
		}
		blocks = append(blocks, MaterialBlock{VideoID: video.VideoID, BlockID: block.ID, Text: strings.TrimSpace(block.Text)})
	}
	return blocks, nil
}
