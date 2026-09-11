package trainingorchestration

import (
	"context"
	"fmt"
	"strings"

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
			ContractVersion: MaterialContractVersion,
			ClusterKey:      cluster.ClusterKey,
			SourceVideoIDs:  append([]string(nil), cluster.SourceVideoIDs...),
			SummaryBlocks:   []MaterialBlock{},
			Evidence:        []MaterialEvidence{},
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
		if err := material.ValidateAgainst(cluster); err != nil {
			return nil, fmt.Errorf("validate material for cluster %s: %w", cluster.ClusterKey, err)
		}
		materials = append(materials, material)
	}
	return materials, nil
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
