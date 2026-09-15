package meetingorchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/promptreload"
)

const (
	JobQueued       = "queued"
	JobRunning      = "running"
	JobSucceeded    = "succeeded"
	JobFailed       = "failed"
	StageCollecting = "collecting"
	StagePublishing = "publishing"
)

type Service struct {
	DB              *gorm.DB
	OwnerScopeID    string
	PromptVersion   string
	Wiki            SummaryReader
	KnowledgeBaseID string
	Prompts         *promptreload.Bundle
	Generator       ProjectionGenerator
	RunTimeout      time.Duration
	mu              sync.Mutex
	running         map[string]struct{}
	projections     map[string]Projection
}

// ProjectionGenerator executes the six prompt stages and must return a fully
// validated projection. The service keeps this seam small so model calls can
// be tested without a live provider.
type ProjectionGenerator interface {
	Generate(context.Context, []model.Video, promptreload.Snapshot) (Projection, error)
}

type SummaryReader interface {
	GetPageByID(context.Context, string, string) (*weknora.WikiPage, error)
}
type ProjectionWriter interface {
	UpsertPage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error)
}

func (s *Service) Start(ctx context.Context) (model.MeetingOrchestrationJob, error) {
	if s == nil || s.DB == nil || strings.TrimSpace(s.OwnerScopeID) == "" {
		return model.MeetingOrchestrationJob{}, errors.New("meeting orchestration is not configured")
	}
	if s.Prompts != nil {
		if snapshot, err := s.Prompts.Load(); err == nil && strings.TrimSpace(snapshot.Version) != "" {
			s.PromptVersion = snapshot.Version
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var active model.MeetingOrchestrationJob
	if err := s.DB.WithContext(ctx).Where("owner_scope_id = ? AND status IN ?", s.OwnerScopeID, []string{JobQueued, JobRunning}).Order("created_at DESC").First(&active).Error; err == nil {
		return active, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.MeetingOrchestrationJob{}, err
	}
	job := model.MeetingOrchestrationJob{ID: uuid.NewString(), OwnerScopeID: s.OwnerScopeID, Status: JobQueued, Stage: StageCollecting, PromptVersion: s.PromptVersion}
	if err := s.DB.WithContext(ctx).Create(&job).Error; err != nil {
		return job, err
	}
	if s.running == nil {
		s.running = map[string]struct{}{}
	}
	s.running[job.ID] = struct{}{}
	go s.run(job.ID)
	return job, nil
}

func (s *Service) GetJob(ctx context.Context, id string) (model.MeetingOrchestrationJob, error) {
	var job model.MeetingOrchestrationJob
	err := s.DB.WithContext(ctx).Where("id = ? AND owner_scope_id = ?", strings.TrimSpace(id), s.OwnerScopeID).First(&job).Error
	return job, err
}

func (s *Service) GetCurrent(ctx context.Context) (*Projection, *model.MeetingOrchestrationCurrent, error) {
	if s == nil || s.DB == nil {
		return nil, nil, errors.New("meeting orchestration is not configured")
	}
	var current model.MeetingOrchestrationCurrent
	if err := s.DB.WithContext(ctx).Where("owner_scope_id = ?", s.OwnerScopeID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	s.mu.Lock()
	projection, ok := s.projections[current.ResultWikiPageID]
	s.mu.Unlock()
	if !ok {
		if s.Wiki != nil {
			if page, err := s.Wiki.GetPageByID(ctx, s.KnowledgeBaseID, current.ResultWikiPageID); err == nil && page != nil {
				var persisted Projection
				if json.Unmarshal([]byte(page.Content), &persisted) == nil && (persisted.SchemaVersion == SchemaVersion || persisted.SchemaVersion == LegacySchemaVersion) && persisted.SourceFingerprint == current.SourceFingerprint {
					if persisted.GeneratedAt.IsZero() {
						persisted.GeneratedAt = current.UpdatedAt
					}
					normalizeProjectionArrays(&persisted)
					if err := persisted.Validate(); err == nil {
						return &persisted, &current, nil
					}
				}
			}
		}
		return nil, &current, errors.New("meeting projection is unavailable; regenerate required")
	}
	if err := projection.Validate(); err != nil {
		return nil, &current, errors.New("meeting projection failed validation; regenerate required")
	}
	return &projection, &current, nil
}

func (s *Service) run(id string) {
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	timeout := s.RunTimeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	now := time.Now().UTC()
	_ = s.DB.WithContext(ctx).Model(&model.MeetingOrchestrationJob{}).Where("id = ?", id).Updates(map[string]any{"status": JobRunning, "stage": StageCollecting, "progress": 10, "started_at": now, "updated_at": now})
	var videos []model.Video
	if err := s.DB.WithContext(ctx).Where("video_type = ? AND uploaded_at IS NOT NULL AND deleted_at IS NULL", "meeting").Order("created_at ASC, id ASC").Find(&videos).Error; err != nil {
		s.fail(id, "input_collection_failed", err)
		return
	}
	qualified := filterQualifiedMeetingVideos(ctx, s.DB, videos)
	fingerprint := fingerprintVideos(qualified)
	var projection Projection
	if s.Generator != nil {
		var snapshot promptreload.Snapshot
		var snapshotErr error
		if s.Prompts != nil {
			snapshot, snapshotErr = s.Prompts.Load()
		}
		if s.Prompts == nil {
			snapshot = promptreload.Snapshot{Version: s.PromptVersion}
		}
		if snapshotErr != nil && snapshot.Version == "" {
			s.fail(id, "prompt_bundle_invalid", snapshotErr)
			return
		}
		generated, genErr := s.Generator.Generate(ctx, qualified, snapshot)
		if errors.Is(genErr, ErrInsufficientEvidence) {
			// A configured AI generator must never fall back to a title-derived
			// projection. That projection has no auditable evidence and would
			// violate the publish gate. Keep the previous current instead.
			s.fail(id, "input_insufficient_evidence", genErr)
			return
		} else if genErr != nil {
			s.fail(id, "model_output_invalid", genErr)
			return
		}
		projection = generated
		projection.OwnerScopeID = s.OwnerScopeID
		projection.SourceFingerprint = fingerprint
		projection.SchemaVersion = SchemaVersion
		projection.GeneratedAt = now
	} else {
		if len(qualified) > 0 {
			s.fail(id, "model_not_configured", errors.New("meeting AI generator is not configured"))
			return
		}
		// With no eligible source there is no content to project. An empty
		// result is a real empty state, not a title-derived fallback.
		projection = Projection{SchemaVersion: SchemaVersion, OwnerScopeID: s.OwnerScopeID, SourceFingerprint: fingerprint, GeneratedAt: time.Now().UTC(), TopicClusters: []TopicCluster{}, TopicRelations: []TopicRelation{}}
	}
	normalizeProjectionArrays(&projection)
	projection.Statistics.ScannedVideos = len(videos)
	projection.Statistics.QualifiedVideos = len(qualified)
	projection.Statistics.SkippedVideos = len(videos) - len(qualified)
	projection.Statistics.MeetingSessionCount = len(projection.MeetingSessions)
	if err := projection.Validate(); err != nil {
		s.fail(id, "projection_invalid", err)
		return
	}
	if err := validateProjectionSources(projection, qualified); err != nil {
		s.fail(id, "projection_invalid", err)
		return
	}
	pageID := "meeting-projection-" + fingerprint[:16]
	if writer, ok := s.Wiki.(ProjectionWriter); ok && strings.TrimSpace(s.KnowledgeBaseID) != "" {
		if content, marshalErr := json.Marshal(projection); marshalErr == nil {
			written, writeErr := writer.UpsertPage(ctx, s.KnowledgeBaseID, weknora.WikiPageWrite{Slug: pageID, Title: "会议主题簇 " + now.Format("2006-01-02 15:04"), PageType: "index", Status: "published", Content: string(content), Summary: "会议跨视频主题簇投影"})
			if writeErr != nil {
				s.fail(id, "publish_failed", writeErr)
				return
			}
			if written != nil && strings.TrimSpace(written.ID) != "" {
				pageID = written.ID
			}
		}
	}
	s.mu.Lock()
	if s.projections == nil {
		s.projections = map[string]Projection{}
	}
	s.projections[pageID] = projection
	s.mu.Unlock()
	now = time.Now().UTC()
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current := model.MeetingOrchestrationCurrent{OwnerScopeID: s.OwnerScopeID, JobID: id, ResultWikiPageID: pageID, SourceFingerprint: fingerprint, UpdatedAt: now}
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		return tx.Model(&model.MeetingOrchestrationJob{}).Where("id = ?", id).Updates(map[string]any{"status": JobSucceeded, "stage": StagePublishing, "progress": 100, "source_fingerprint": fingerprint, "result_wiki_page_id": pageID, "finished_at": now, "updated_at": now}).Error
	})
	if err != nil {
		s.fail(id, "publish_failed", err)
	}
}

func (s *Service) fail(id, code string, err error) {
	now := time.Now().UTC()
	code = meetingErrorCode(code, err)
	_ = s.DB.Model(&model.MeetingOrchestrationJob{}).Where("id = ?", id).Updates(map[string]any{"status": JobFailed, "error_code": code, "error_message": safeMeetingErrorMessage(code), "finished_at": now, "updated_at": now})
}

func fingerprintVideos(videos []model.Video) string {
	h := sha256.New()
	for _, v := range videos {
		fmt.Fprintf(h, "%s|%s|%d|%s;", v.ID, v.Title, v.SummaryWikiPageVersion, v.TranscriptGeneration)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func filterQualifiedMeetingVideos(ctx context.Context, db *gorm.DB, videos []model.Video) []model.Video {
	if db == nil {
		return nil
	}
	qualified := make([]model.Video, 0, len(videos))
	for _, video := range videos {
		if strings.TrimSpace(video.SummaryWikiPageID) == "" || strings.TrimSpace(video.TranscriptGeneration) == "" {
			continue
		}
		var count int64
		if err := db.WithContext(ctx).Model(&model.VideoTranscriptChunk{}).Where("video_id = ? AND generation = ? AND status = ? AND evidence_sentence_id <> ''", video.ID, video.TranscriptGeneration, "completed").Count(&count).Error; err != nil {
			continue
		}
		// The legacy TranscriptKnowledgeID is a collection anchor, not a
		// seekable evidence sentence. A video without current chunks must be
		// skipped instead of being treated as qualified.
		if count == 0 {
			continue
		}
		qualified = append(qualified, video)
	}
	return qualified
}

func validateProjectionSources(projection Projection, videos []model.Video) error {
	allowedVideos := make(map[string]string, len(videos))
	for _, video := range videos {
		id := strings.TrimSpace(video.ID)
		generation := strings.TrimSpace(video.TranscriptGeneration)
		if id == "" || generation == "" {
			continue
		}
		allowedVideos[id] = generation
	}
	for _, cluster := range projection.TopicClusters {
		seenVideoIDs := map[string]struct{}{}
		for _, videoID := range cluster.SourceVideoIDs {
			videoID = strings.TrimSpace(videoID)
			if videoID == "" {
				return errors.New("projection contains an empty source video")
			}
			if _, ok := allowedVideos[videoID]; !ok {
				return fmt.Errorf("projection source video %q is not in current input", videoID)
			}
			if _, duplicate := seenVideoIDs[videoID]; duplicate {
				return fmt.Errorf("projection source video %q is duplicated", videoID)
			}
			seenVideoIDs[videoID] = struct{}{}
		}
		for _, contribution := range cluster.VideoContributions {
			if _, ok := allowedVideos[strings.TrimSpace(contribution.VideoID)]; !ok {
				return fmt.Errorf("projection contribution video %q is not in current input", contribution.VideoID)
			}
			if err := validateProjectionRefsAgainstVideos(contribution.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
		for _, item := range cluster.WorkItems {
			if err := validateProjectionRefsAgainstVideos(item.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
		for _, event := range cluster.Evolution {
			if err := validateProjectionRefsAgainstVideos(event.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
		for _, decision := range cluster.Decisions {
			if err := validateProjectionRefsAgainstVideos(decision.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
		for _, todo := range cluster.Todos {
			if err := validateProjectionRefsAgainstVideos(todo.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
	}
	for _, session := range projection.MeetingSessions {
		for _, videoID := range session.FragmentVideoIDs {
			if _, ok := allowedVideos[strings.TrimSpace(videoID)]; !ok {
				return fmt.Errorf("projection session video %q is not in current input", videoID)
			}
		}
		if err := validateProjectionRefsAgainstVideos(session.GroupingEvidenceRefs, allowedVideos); err != nil {
			return err
		}
	}
	for _, relation := range projection.TopicRelations {
		if err := validateProjectionRefsAgainstVideos(relation.SourceEvidenceRefs, allowedVideos); err != nil {
			return err
		}
		if err := validateProjectionRefsAgainstVideos(relation.TargetEvidenceRefs, allowedVideos); err != nil {
			return err
		}
		if len(relation.SourceEvidenceRefs) == 0 && len(relation.TargetEvidenceRefs) == 0 {
			if err := validateProjectionRefsAgainstVideos(relation.EvidenceRefs, allowedVideos); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProjectionRefsAgainstVideos(refs []EvidenceRef, allowedVideos map[string]string) error {
	for _, ref := range refs {
		generation, ok := allowedVideos[strings.TrimSpace(ref.VideoID)]
		if !ok {
			return fmt.Errorf("projection evidence video %q is not in current input", ref.VideoID)
		}
		if strings.TrimSpace(ref.TranscriptGeneration) != generation {
			return fmt.Errorf("projection evidence generation is not current for video %q", ref.VideoID)
		}
	}
	return nil
}

var nonWord = regexp.MustCompile(`[：:，,。！？!?（）()【】\[\]、/\\_\-]+`)

func businessObject(title string) string {
	title = strings.TrimSpace(title)
	// Meeting-stage suffixes describe the conversation, not the managed
	// business object. Prefer the stable project/product phrase when the title
	// explicitly names one, so demand, kickoff, review and launch meetings can
	// share one object cluster in the no-AI fallback path.
	if projectEnd := strings.Index(title, "项目"); projectEnd >= 2 {
		return strings.TrimSpace(title[:projectEnd+len("项目")])
	}
	stageStripped := false
	for _, suffix := range []string{"发布上线沟通会议", "发布上线沟通会", "方案评审会", "技术评审会", "需求沟通会", "启动会", "评审会", "沟通会", "会议"} {
		if strings.HasSuffix(title, suffix) {
			title = strings.TrimSpace(strings.TrimSuffix(title, suffix))
			stageStripped = true
			break
		}
	}
	runes := []rune(title)
	if stageStripped && len(runes) >= 2 {
		return title
	}
	if len(runes) >= 2 && runes[0] >= '\u4e00' && runes[0] <= '\u9fff' && runes[1] >= '\u4e00' && runes[1] <= '\u9fff' {
		return string(runes[:2])
	}
	parts := strings.FieldsFunc(strings.TrimSpace(title), func(r rune) bool { return nonWord.MatchString(string(r)) || r == ' ' })
	if len(parts) == 0 {
		return "未命名业务对象"
	}
	value := parts[0]
	if len([]rune(value)) < 2 && len(parts) > 1 {
		value += parts[1]
	}
	return value
}

func (s *Service) buildProjection(ctx context.Context, fingerprint string, videos []model.Video) Projection {
	groups := map[string][]model.Video{}
	keys := []string{}
	for _, v := range videos {
		key := s.businessObjectForVideo(ctx, v)
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], v)
	}
	// A cycle-only title such as "7 月 10 日上线" carries no object name. If
	// the batch has one explicit business-object group, attach the cycle item
	// to that group; otherwise keep it separate and require an AI candidate
	// with evidence to resolve the ambiguity.
	if len(groups) > 1 {
		explicit := make([]string, 0, len(groups))
		for key := range groups {
			if key != "__cycle__" {
				explicit = append(explicit, key)
			}
		}
		if len(explicit) == 1 {
			if cycle, ok := groups["__cycle__"]; ok {
				groups[explicit[0]] = append(groups[explicit[0]], cycle...)
				delete(groups, "__cycle__")
			}
		}
	}
	keys = keys[:0]
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	clusters := make([]TopicCluster, 0, len(keys))
	sessions := make([]MeetingSession, 0, len(videos))
	decisions, todos := 0, 0
	for _, key := range keys {
		vs := groups[key]
		c := TopicCluster{ClusterID: stableClusterID(key, key), Title: key, BusinessObject: key, Summary: fmt.Sprintf("围绕%s推进的会议事项与决策。", key)}
		for _, v := range vs {
			sessionID := stableSessionID(v.ID)
			sessions = append(sessions, MeetingSession{MeetingSessionID: sessionID, FragmentVideoIDs: []string{v.ID}, OrderingBasis: "single_fragment_content_unavailable", GroupingEvidenceRefs: []EvidenceRef{}})
			c.SourceVideoIDs = append(c.SourceVideoIDs, v.ID)
			c.MeetingSessionIDs = append(c.MeetingSessionIDs, sessionID)
			c.VideoContributions = append(c.VideoContributions, VideoContribution{VideoID: v.ID, MeetingSessionID: sessionID, ContributionType: "meeting_record", EvidenceRefs: []EvidenceRef{}})
			c.Evolution = append(c.Evolution, EvolutionEvent{ID: "event-" + v.ID, VideoID: v.ID, MeetingSessionID: sessionID, MeetingTitle: v.Title, Summary: "已纳入会议主题簇，等待事项级证据更新。", Change: "新增会议记录"})
			c.WorkItems = append(c.WorkItems, WorkItem{ID: "item-" + v.ID, Title: v.Title, Status: "unknown"})
		}
		clusters = append(clusters, c)
	}
	return Projection{SchemaVersion: SchemaVersion, OwnerScopeID: s.OwnerScopeID, SourceFingerprint: fingerprint, GeneratedAt: time.Now().UTC(), Statistics: Statistics{ScannedVideos: len(videos), QualifiedVideos: len(videos), MeetingSessionCount: len(sessions), TopicClusterCount: len(clusters), DecisionCount: decisions, TodoCount: todos}, MeetingSessions: sessions, TopicClusters: clusters, TopicRelations: []TopicRelation{}}
}

func (s *Service) businessObjectForVideo(ctx context.Context, video model.Video) string {
	if s != nil && s.Wiki != nil && strings.TrimSpace(s.KnowledgeBaseID) != "" && strings.TrimSpace(video.SummaryWikiPageID) != "" {
		if page, err := s.Wiki.GetPageByID(ctx, s.KnowledgeBaseID, video.SummaryWikiPageID); err == nil && page != nil {
			frontmatter := page.ParsedFrontmatter()
			for _, key := range []string{"meeting_tags", "tags", "labels"} {
				if raw, ok := frontmatter[key].([]any); ok && len(raw) > 0 {
					if tag, ok := raw[0].(string); ok && strings.TrimSpace(tag) != "" {
						return strings.TrimSpace(tag)
					}
				}
				if tag, ok := frontmatter[key].(string); ok && strings.TrimSpace(tag) != "" {
					return strings.TrimSpace(tag)
				}
			}
		}
	}
	if strings.Contains(video.Title, "上线") && strings.ContainsAny(video.Title, "0123456789０１２３４５６７８９") {
		return "__cycle__"
	}
	return businessObject(video.Title)
}
