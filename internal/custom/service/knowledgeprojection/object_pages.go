package knowledgeprojection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"gopkg.in/yaml.v3"
)

type EvidenceProjection struct {
	CandidateID          string   `json:"candidate_id"`
	SourceVideoID        string   `json:"source_video_id"`
	SourceVideoTitle     string   `json:"source_video_title"`
	TranscriptGeneration string   `json:"transcript_generation"`
	EvidenceIDs          []string `json:"evidence_ids"`
	ChunkRefs            []string `json:"chunk_refs"`
	SourceRefs           []string `json:"source_refs"`
	TimeRange            string   `json:"time_range"`
}

type ObjectPageInput struct {
	Object        knowledge.ClassifiedKnowledge `json:"object"`
	Projection    EvidenceProjection            `json:"projection"`
	FieldEvidence map[string][]string           `json:"field_evidence"`
}

type ObjectPageResult struct {
	CandidateID          string                  `json:"candidate_id"`
	KnowledgeObjectID    string                  `json:"knowledge_object_id"`
	PrimaryType          knowledge.KnowledgeType `json:"primary_type"`
	WikiPageID           string                  `json:"wiki_page_id"`
	Slug                 string                  `json:"slug"`
	Version              int                     `json:"version"`
	Action               string                  `json:"action"`
	ContentSHA256        string                  `json:"content_sha256"`
	SourceVideoID        string                  `json:"source_video_id"`
	SourceVideoTitle     string                  `json:"source_video_title"`
	TranscriptGeneration string                  `json:"transcript_generation"`
}

type Wiki interface {
	ListAllPages(context.Context, string, string) ([]weknora.WikiPage, error)
	GetPage(context.Context, string, string) (*weknora.WikiPage, error)
	EnsurePage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error)
	UpsertPage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error)
}

type ObjectPagePublisher struct {
	Wiki                         Wiki
	KBID                         string
	ExpectedVideoID              string
	ExpectedSourceVideoTitle     string
	ExpectedSourceDocumentID     string
	ExpectedTranscriptGeneration string
	ExpectedVideoDurationMs      int64
}

type preparedPage struct {
	input    ObjectPageInput
	render   knowledge.FirstStagePageRender
	slug     string
	existing *weknora.WikiPage
}

// PublishFirstStage writes relation-free object pages and reads every result
// back by WeKnora's actual slug and page ID. A partial result is returned when
// a later external write fails, so successful writes remain auditable.
func (p ObjectPagePublisher) PublishFirstStage(ctx context.Context, inputs []ObjectPageInput) ([]ObjectPageResult, error) {
	if p.Wiki == nil || strings.TrimSpace(p.KBID) == "" {
		return nil, fmt.Errorf("object page publisher requires Wiki client and knowledge base")
	}
	videoID := strings.TrimSpace(p.ExpectedVideoID)
	videoTitle := strings.TrimSpace(p.ExpectedSourceVideoTitle)
	sourceDocumentID := strings.TrimSpace(p.ExpectedSourceDocumentID)
	generation := strings.TrimSpace(p.ExpectedTranscriptGeneration)
	if videoID == "" || videoTitle == "" || sourceDocumentID == "" || generation == "" || p.ExpectedVideoDurationMs <= 0 {
		return nil, fmt.Errorf("object page publisher requires active video, title, duration, source document and transcript generation scope")
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("object page publisher requires at least one object")
	}
	pages, err := p.Wiki.ListAllPages(ctx, p.KBID, "")
	if err != nil {
		return nil, fmt.Errorf("list existing object pages: %w", err)
	}
	existingByIdentity, existingBySlug, err := indexExistingPages(pages)
	if err != nil {
		return nil, err
	}

	prepared := make([]preparedPage, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	seenSlugs := make(map[string]string, len(inputs))
	for _, input := range inputs {
		if err := validateInput(input, videoID, videoTitle, sourceDocumentID, generation, p.ExpectedVideoDurationMs); err != nil {
			return nil, err
		}
		identity := pageIdentity(input.Object.SourceVideoID, input.Object.TranscriptGeneration, input.Object.CandidateID)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("duplicate object page input %q", input.Object.CandidateID)
		}
		seen[identity] = struct{}{}
		render, err := knowledge.RenderFirstStageObjectPage(knowledge.FirstStagePageInput{
			Object: input.Object, TimeRange: input.Projection.TimeRange,
			ChunkRefs: input.Projection.ChunkRefs, FieldEvidence: input.FieldEvidence,
		})
		if err != nil {
			return nil, fmt.Errorf("render object %s: %w", input.Object.CandidateID, err)
		}
		expected := expectation(input, p.ExpectedVideoDurationMs)
		if err := knowledge.ValidateFirstStageWikiObjectPage(render.Content, render.PageType, expected); err != nil {
			return nil, fmt.Errorf("validate object %s first-stage page: %w", input.Object.CandidateID, err)
		}
		item := preparedPage{input: input, render: render, slug: stablePageSlug(input.Object)}
		if existing := existingByIdentity[identity]; existing != nil {
			if err := validateExistingType(*existing, input.Object); err != nil {
				return nil, err
			}
			item.existing = existing
			item.slug = existing.Slug
		} else if occupied := existingBySlug[item.slug]; occupied != nil {
			return nil, fmt.Errorf("stable slug %q is already occupied by another Wiki page", item.slug)
		}
		if prior, duplicate := seenSlugs[item.slug]; duplicate && prior != identity {
			return nil, fmt.Errorf("stable slug %q collides for object %s", item.slug, input.Object.CandidateID)
		}
		seenSlugs[item.slug] = identity
		prepared = append(prepared, item)
	}
	sort.SliceStable(prepared, func(i, j int) bool {
		return prepared[i].input.Object.CandidateID < prepared[j].input.Object.CandidateID
	})

	results := make([]ObjectPageResult, 0, len(prepared))
	seenPageIDs := make(map[string]string, len(prepared))
	seenResultSlugs := make(map[string]string, len(prepared))
	for _, item := range prepared {
		current := item.existing
		if current != nil {
			current, err = p.Wiki.GetPage(ctx, p.KBID, current.Slug)
			if err != nil || current == nil || current.ID == "" {
				return results, fmt.Errorf("read existing object %s: %w", item.input.Object.CandidateID, firstError(err, fmt.Errorf("page is not readable")))
			}
		}
		action := "created"
		written := current
		write := pageWrite(item)
		if current != nil && pageMatchesWrite(*current, write) {
			action = "unchanged"
		} else {
			if current != nil {
				action = "updated"
				write.Version = current.Version
				written, err = p.Wiki.UpsertPage(ctx, p.KBID, write)
			} else {
				// A missing identity is a create path. EnsurePage avoids the
				// read-then-post race that UpsertPage would introduce.
				written, err = p.Wiki.EnsurePage(ctx, p.KBID, write)
			}
			if err != nil {
				// A concurrent create may have won under a server-generated slug.
				// Recover only from a page with the same object identity and exact
				// payload; all other errors remain visible to the caller.
				raced, recoverErr := p.recoverConcurrentCreate(ctx, write, item.input)
				if recoverErr != nil {
					return results, fmt.Errorf("%s object page %s: %w (original write: %v)", action, item.input.Object.CandidateID, recoverErr, err)
				}
				written = raced
				action = "unchanged"
			}
		}
		if written == nil || written.ID == "" || written.Slug == "" {
			return results, fmt.Errorf("object %s write returned incomplete page identity", item.input.Object.CandidateID)
		}
		readBack, err := p.Wiki.GetPage(ctx, p.KBID, written.Slug)
		if err != nil {
			return results, fmt.Errorf("read back object %s: %w", item.input.Object.CandidateID, err)
		}
		if err := validateReadBack(readBack, written.ID, written.Slug, write, item.input, p.ExpectedVideoDurationMs); err != nil {
			return results, fmt.Errorf("read back object %s: %w", item.input.Object.CandidateID, err)
		}
		if prior, duplicate := seenPageIDs[readBack.ID]; duplicate && prior != item.input.Object.CandidateID {
			return results, fmt.Errorf("Wiki page ID %q is shared by objects %s and %s", readBack.ID, prior, item.input.Object.CandidateID)
		}
		if prior, duplicate := seenResultSlugs[readBack.Slug]; duplicate && prior != item.input.Object.CandidateID {
			return results, fmt.Errorf("Wiki page slug %q is shared by objects %s and %s", readBack.Slug, prior, item.input.Object.CandidateID)
		}
		seenPageIDs[readBack.ID] = item.input.Object.CandidateID
		seenResultSlugs[readBack.Slug] = item.input.Object.CandidateID
		digest := sha256.Sum256([]byte(readBack.Content))
		results = append(results, ObjectPageResult{
			CandidateID: item.input.Object.CandidateID, KnowledgeObjectID: item.input.Object.CandidateID,
			PrimaryType: item.input.Object.PrimaryType, WikiPageID: readBack.ID, Slug: readBack.Slug,
			Version: readBack.Version, Action: action, ContentSHA256: hex.EncodeToString(digest[:]),
			SourceVideoID: item.input.Object.SourceVideoID, TranscriptGeneration: item.input.Object.TranscriptGeneration,
			SourceVideoTitle: item.input.Projection.SourceVideoTitle,
		})
	}
	for _, result := range results {
		page, err := p.Wiki.GetPage(ctx, p.KBID, result.Slug)
		if err != nil || page == nil {
			return results, fmt.Errorf("final read back object %s: %w", result.CandidateID, firstError(err, fmt.Errorf("page is not readable")))
		}
		if len(page.InLinks) != 0 || len(page.OutLinks) != 0 {
			return results, fmt.Errorf("final first-stage page %s must not have Wiki links", result.CandidateID)
		}
	}
	return results, nil
}

func validateInput(input ObjectPageInput, videoID, videoTitle, sourceDocumentID, generation string, durationMs int64) error {
	object, projection := input.Object, input.Projection
	if err := knowledge.ValidatePublishGate(object); err != nil {
		return fmt.Errorf("object %s failed publish gate: %w", object.CandidateID, err)
	}
	if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
		return fmt.Errorf("object %s audit_status must be passed", object.CandidateID)
	}
	if object.SourceVideoID != videoID || object.SourceDocumentID != sourceDocumentID || object.TranscriptGeneration != generation {
		return fmt.Errorf("object %s does not belong to the active video generation", object.CandidateID)
	}
	if projection.CandidateID != object.CandidateID || projection.SourceVideoID != object.SourceVideoID || projection.TranscriptGeneration != object.TranscriptGeneration {
		return fmt.Errorf("object %s evidence projection identity mismatch", object.CandidateID)
	}
	if !sameValues(projection.EvidenceIDs, object.EvidenceIDs) {
		return fmt.Errorf("object %s evidence projection does not match passed object", object.CandidateID)
	}
	if len(projection.SourceRefs) != 1 || projection.SourceRefs[0] != object.SourceDocumentID {
		return fmt.Errorf("object %s source_refs must contain its source document", object.CandidateID)
	}
	if len(projection.ChunkRefs) == 0 || strings.TrimSpace(projection.TimeRange) == "" || strings.TrimSpace(projection.SourceVideoTitle) == "" {
		return fmt.Errorf("object %s evidence projection is incomplete", object.CandidateID)
	}
	if err := validateReferenceList(projection.ChunkRefs, "chunk_refs"); err != nil {
		return fmt.Errorf("object %s: %w", object.CandidateID, err)
	}
	if err := validateReferenceList(projection.SourceRefs, "source_refs"); err != nil {
		return fmt.Errorf("object %s: %w", object.CandidateID, err)
	}
	if err := knowledge.ValidateFirstStageTimeRange(projection.TimeRange, durationMs); err != nil {
		return fmt.Errorf("object %s: %w", object.CandidateID, err)
	}
	if strings.TrimSpace(projection.SourceVideoTitle) != videoTitle {
		return fmt.Errorf("object %s source video title does not match active video", object.CandidateID)
	}
	if err := knowledge.ValidateFirstStageFieldEvidence(object, input.FieldEvidence); err != nil {
		return fmt.Errorf("object %s: %w", object.CandidateID, err)
	}
	return nil
}

func expectation(input ObjectPageInput, durationMs int64) knowledge.FirstStagePageExpectation {
	return knowledge.FirstStagePageExpectation{Object: input.Object, TimeRange: input.Projection.TimeRange,
		VideoDurationMs: durationMs, ChunkRefs: input.Projection.ChunkRefs, SourceRefs: input.Projection.SourceRefs}
}

func indexExistingPages(pages []weknora.WikiPage) (map[string]*weknora.WikiPage, map[string]*weknora.WikiPage, error) {
	result := make(map[string]*weknora.WikiPage)
	bySlug := make(map[string]*weknora.WikiPage, len(pages))
	for i := range pages {
		page := pages[i]
		if prior := bySlug[page.Slug]; prior != nil && prior.ID != page.ID {
			return nil, nil, fmt.Errorf("duplicate Wiki slug %q", page.Slug)
		}
		bySlug[page.Slug] = &page
		frontmatter := parseFrontmatter(pages[i].Content)
		objectID := scalar(frontmatter["knowledge_object_id"])
		videoID := scalar(frontmatter["source_video_id"])
		generation := scalar(frontmatter["transcript_generation"])
		if objectID == "" || videoID == "" || generation == "" {
			continue
		}
		identity := pageIdentity(videoID, generation, objectID)
		if prior := result[identity]; prior != nil {
			return nil, nil, fmt.Errorf("duplicate Wiki pages for knowledge object %s", objectID)
		}
		result[identity] = &page
	}
	return result, bySlug, nil
}

func validateExistingType(page weknora.WikiPage, object knowledge.ClassifiedKnowledge) error {
	frontmatter := parseFrontmatter(page.Content)
	primaryType := strings.ToLower(scalar(frontmatter["primary_type"]))
	if primaryType == "" {
		primaryType = strings.ToLower(scalar(frontmatter["type"]))
	}
	if primaryType != string(object.PrimaryType) {
		return fmt.Errorf("existing object %s primary_type conflicts with publish input", object.CandidateID)
	}
	if object.PrimaryType == knowledge.TypeEntity && strings.ToLower(scalar(frontmatter["entity_sub_type"])) != strings.ToLower(strings.TrimSpace(object.EntitySubType)) {
		return fmt.Errorf("existing object %s entity_sub_type conflicts with publish input", object.CandidateID)
	}
	return nil
}

func pageWrite(item preparedPage) weknora.WikiPageWrite {
	return weknora.WikiPageWrite{Slug: item.slug, Title: item.render.Title, PageType: item.render.PageType,
		Status: "published", Content: item.render.Content, Summary: strings.TrimSpace(item.input.Object.CoreContent),
		SourceRefs: sortedValues(item.input.Projection.SourceRefs), ChunkRefs: sortedValues(item.input.Projection.ChunkRefs)}
}

func pageMatchesWrite(page weknora.WikiPage, write weknora.WikiPageWrite) bool {
	return page.Slug == write.Slug && pagePayloadMatchesWrite(page, write)
}

func pagePayloadMatchesWrite(page weknora.WikiPage, write weknora.WikiPageWrite) bool {
	return page.Title == write.Title && page.PageType == write.PageType &&
		page.Status == write.Status && page.Content == write.Content && page.Summary == write.Summary &&
		sameValues(page.SourceRefs, write.SourceRefs) && sameValues(page.ChunkRefs, write.ChunkRefs)
}

func validateReadBack(page *weknora.WikiPage, expectedID, actualSlug string, write weknora.WikiPageWrite, input ObjectPageInput, durationMs int64) error {
	if page == nil || page.ID == "" || page.ID != expectedID || page.Version < 1 || page.Slug != actualSlug {
		return fmt.Errorf("Wiki page ID does not match write response")
	}
	if !pagePayloadMatchesWrite(*page, write) {
		return fmt.Errorf("Wiki page content or source metadata does not match write input")
	}
	if page.Status != "published" {
		return fmt.Errorf("first-stage Wiki page status must be published")
	}
	if len(page.InLinks) != 0 || len(page.OutLinks) != 0 {
		return fmt.Errorf("first-stage Wiki page in_links and out_links must be empty")
	}
	return knowledge.ValidateFirstStageWikiObjectPage(page.Content, page.PageType, expectation(input, durationMs))
}

func (p ObjectPagePublisher) recoverConcurrentCreate(ctx context.Context, write weknora.WikiPageWrite, input ObjectPageInput) (*weknora.WikiPage, error) {
	if raced, err := p.Wiki.GetPage(ctx, p.KBID, write.Slug); err == nil && raced != nil && pageIdentityFromContent(raced.Content) == pageIdentity(input.Object.SourceVideoID, input.Object.TranscriptGeneration, input.Object.CandidateID) {
		if pagePayloadMatchesWrite(*raced, write) {
			return raced, nil
		}
		return nil, fmt.Errorf("concurrent page at slug %q does not match requested payload", write.Slug)
	}
	pages, err := p.Wiki.ListAllPages(ctx, p.KBID, "")
	if err != nil {
		return nil, fmt.Errorf("list pages after concurrent create: %w", err)
	}
	targetIdentity := pageIdentity(input.Object.SourceVideoID, input.Object.TranscriptGeneration, input.Object.CandidateID)
	var candidate *weknora.WikiPage
	for i := range pages {
		page := &pages[i]
		if pageIdentityFromContent(page.Content) != targetIdentity {
			continue
		}
		if candidate != nil && candidate.ID != page.ID {
			return nil, fmt.Errorf("concurrent create produced duplicate pages for object %s", input.Object.CandidateID)
		}
		candidate = page
	}
	if candidate == nil {
		return nil, fmt.Errorf("concurrent create page for object %s was not readable", input.Object.CandidateID)
	}
	readBack, err := p.Wiki.GetPage(ctx, p.KBID, candidate.Slug)
	if err != nil {
		return nil, fmt.Errorf("read concurrent page %s: %w", candidate.Slug, err)
	}
	if readBack == nil || pageIdentityFromContent(readBack.Content) != targetIdentity || !pagePayloadMatchesWrite(*readBack, write) {
		return nil, fmt.Errorf("concurrent page %s does not match requested identity or payload", candidate.Slug)
	}
	return readBack, nil
}

func validateReferenceList(values []string, field string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s must not contain empty values", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s must not contain duplicate values", field)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func stablePageSlug(object knowledge.ClassifiedKnowledge) string {
	digest := sha256.Sum256([]byte(pageIdentity(object.SourceVideoID, object.TranscriptGeneration, object.CandidateID)))
	return fmt.Sprintf("knowledge-object/%s/%s", object.PrimaryType, hex.EncodeToString(digest[:12]))
}

func pageIdentity(videoID, generation, objectID string) string {
	return strings.TrimSpace(videoID) + "\x00" + strings.TrimSpace(generation) + "\x00" + strings.TrimSpace(objectID)
}

func parseFrontmatter(content string) map[string]any {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]any{}
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return map[string]any{}
	}
	result := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &result); err != nil {
		return map[string]any{}
	}
	return result
}

func pageIdentityFromContent(content string) string {
	frontmatter := parseFrontmatter(content)
	videoID := scalar(frontmatter["source_video_id"])
	generation := scalar(frontmatter["transcript_generation"])
	objectID := scalar(frontmatter["knowledge_object_id"])
	if videoID == "" || generation == "" || objectID == "" {
		return ""
	}
	return pageIdentity(videoID, generation, objectID)
}

func scalar(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return ""
	}
}

func sameValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left, right = sortedValues(left), sortedValues(right)
	for i := range left {
		if left[i] == "" || left[i] != right[i] {
			return false
		}
	}
	return true
}

func sortedValues(values []string) []string {
	result := append([]string(nil), values...)
	for i := range result {
		result[i] = strings.TrimSpace(result[i])
	}
	sort.Strings(result)
	return result
}

func firstError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
