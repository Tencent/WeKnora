package meetingorchestration

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/promptreload"
)

type insufficientEvidenceGenerator struct{}

func (insufficientEvidenceGenerator) Generate(context.Context, []model.Video, promptreload.Snapshot) (Projection, error) {
	return Projection{}, ErrInsufficientEvidence
}

type blockingMeetingGenerator struct{}

func (blockingMeetingGenerator) Generate(ctx context.Context, _ []model.Video, _ promptreload.Snapshot) (Projection, error) {
	<-ctx.Done()
	return Projection{}, ctx.Err()
}

func TestServiceBuildsMeetingProjectionFromMeetingVideos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:meeting-orchestration-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.MeetingOrchestrationJob{}, &model.MeetingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	videos := []model.Video{
		{ID: "v1", Title: "客户权限审批流改造", VideoType: "meeting", SummaryWikiPageVersion: 1, TranscriptGeneration: "g1", UploadedAt: &now, Status: model.VideoStatusReady},
		{ID: "v2", Title: "客户数据迁移方案", VideoType: "meeting", SummaryWikiPageVersion: 1, TranscriptGeneration: "g2", UploadedAt: &now, Status: model.VideoStatusReady},
		{ID: "v3", Title: "客户支持体系", VideoType: "meeting", SummaryWikiPageVersion: 1, TranscriptGeneration: "g3", UploadedAt: &now, Status: model.VideoStatusReady},
	}
	service := &Service{DB: db, OwnerScopeID: "scope"}
	projection := service.buildProjection(context.Background(), fingerprintVideos(videos), videos)
	if projection.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q", projection.SchemaVersion)
	}
	if len(projection.TopicClusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(projection.TopicClusters))
	}
	if len(projection.TopicClusters[0].WorkItems) != len(videos) {
		t.Fatalf("work items = %d, want %d", len(projection.TopicClusters[0].WorkItems), len(videos))
	}
}

func TestServiceGroupsCycleTitleWithSingleBusinessObject(t *testing.T) {
	now := time.Now().UTC()
	videos := []model.Video{{ID: "v1", Title: "客户权限审批流改造", VideoType: "meeting", UploadedAt: &now}, {ID: "v2", Title: "7 月 10 日上线", VideoType: "meeting", UploadedAt: &now}}
	service := &Service{OwnerScopeID: "scope"}
	projection := service.buildProjection(context.Background(), fingerprintVideos(videos), videos)
	if len(projection.TopicClusters) != 1 || len(projection.TopicClusters[0].WorkItems) != 2 {
		t.Fatalf("cycle title was not grouped: %#v", projection.TopicClusters)
	}
}

func TestServiceGroupsProjectMeetingStagesByStableBusinessObject(t *testing.T) {
	now := time.Now().UTC()
	videos := []model.Video{
		{ID: "consumer-loan-demand", Title: "消费贷项目需求沟通会", VideoType: "meeting", UploadedAt: &now},
		{ID: "consumer-loan-kickoff", Title: "消费贷项目启动会", VideoType: "meeting", UploadedAt: &now},
		{ID: "consumer-loan-solution", Title: "消费贷项目方案评审会", VideoType: "meeting", UploadedAt: &now},
		{ID: "consumer-loan-technical", Title: "消费贷项目技术评审会", VideoType: "meeting", UploadedAt: &now},
		{ID: "consumer-loan-launch", Title: "消费贷项目发布上线沟通会议", VideoType: "meeting", UploadedAt: &now},
	}
	service := &Service{OwnerScopeID: "scope"}
	projection := service.buildProjection(context.Background(), fingerprintVideos(videos), videos)
	if len(projection.TopicClusters) != 1 {
		t.Fatalf("clusters = %d, want 1: %#v", len(projection.TopicClusters), projection.TopicClusters)
	}
	cluster := projection.TopicClusters[0]
	if cluster.Title != "消费贷项目" || cluster.BusinessObject != "消费贷项目" {
		t.Fatalf("stable business object = %q/%q", cluster.Title, cluster.BusinessObject)
	}
	if len(cluster.SourceVideoIDs) != len(videos) || len(cluster.WorkItems) != len(videos) || len(cluster.Evolution) != len(videos) {
		t.Fatalf("project stages were not retained: source=%d items=%d evolution=%d", len(cluster.SourceVideoIDs), len(cluster.WorkItems), len(cluster.Evolution))
	}
}

func TestServiceIgnoresNonMeetingVideos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:meeting-orchestration-test-2?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.MeetingOrchestrationJob{}, &model.MeetingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, OwnerScopeID: "scope"}
	if err := db.Create(&model.Video{ID: "training", Title: "培训", VideoType: "training", UploadedAt: func() *time.Time { value := time.Now(); return &value }()}).Error; err != nil {
		t.Fatal(err)
	}
	job, err := service.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _, readErr := service.GetCurrent(context.Background())
		if readErr == nil && current != nil {
			if current.Statistics.ScannedVideos != 0 {
				t.Fatalf("scanned = %d", current.Statistics.ScannedVideos)
			}
			return
		}
		time.Sleep(time.Millisecond * 10)
		_ = job
	}
	t.Fatal("meeting job did not publish")
}

func TestServiceAcceptsPromptBundleFingerprintAsJobVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:meeting-orchestration-prompt-version?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.MeetingOrchestrationJob{}, &model.MeetingOrchestrationCurrent{}, &model.Video{}); err != nil {
		t.Fatal(err)
	}
	bundle := promptreload.New("", map[string]string{"prompt.txt": "prompt"})
	snapshot, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Version) <= 64 {
		t.Fatalf("test requires a prefixed fingerprint longer than the legacy column: %q", snapshot.Version)
	}
	service := &Service{DB: db, OwnerScopeID: "scope", Prompts: bundle}
	job, err := service.Start(context.Background())
	if err != nil {
		t.Fatalf("start meeting job with bundle fingerprint: %v", err)
	}
	if job.PromptVersion != snapshot.Version {
		t.Fatalf("prompt version = %q, want %q", job.PromptVersion, snapshot.Version)
	}
}

func TestServiceDoesNotPublishTitleFallbackWhenEvidenceIsInsufficient(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:meeting-orchestration-test-insufficient?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.MeetingOrchestrationJob{}, &model.MeetingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	video := model.Video{ID: "v1", Title: "客户项目会议", VideoType: "meeting", SummaryWikiPageID: "summary-1", SummaryWikiPageVersion: 1, TranscriptGeneration: "g1", UploadedAt: &now, Status: model.VideoStatusReady}
	if err := db.Create(&video).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{VideoID: video.ID, Generation: video.TranscriptGeneration, Status: "completed", EvidenceSentenceID: "e1", StartMs: 0, EndMs: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, OwnerScopeID: "scope", Generator: insufficientEvidenceGenerator{}}
	job, err := service.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var currentJob model.MeetingOrchestrationJob
		if err := db.First(&currentJob, "id = ?", job.ID).Error; err == nil && currentJob.Status == JobFailed {
			if currentJob.ErrorCode != "input_insufficient_evidence" {
				t.Fatalf("error code = %q", currentJob.ErrorCode)
			}
			projection, current, currentErr := service.GetCurrent(context.Background())
			if currentErr != nil || projection != nil || current != nil {
				t.Fatal("title-derived fallback was published")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("meeting job did not fail")
}

func TestServiceUsesConfiguredRunTimeoutWithoutPublishing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:meeting-orchestration-timeout?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.MeetingOrchestrationJob{}, &model.MeetingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	video := model.Video{ID: "v-timeout", Title: "客户项目会议", VideoType: "meeting", SummaryWikiPageID: "summary-1", TranscriptGeneration: "g1", UploadedAt: &now, Status: model.VideoStatusReady}
	if err := db.Create(&video).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{VideoID: video.ID, Generation: video.TranscriptGeneration, Status: "completed", EvidenceSentenceID: "e1", StartMs: 0, EndMs: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, OwnerScopeID: "scope", Generator: blockingMeetingGenerator{}, RunTimeout: 20 * time.Millisecond}
	job, err := service.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var currentJob model.MeetingOrchestrationJob
		if err := db.First(&currentJob, "id = ?", job.ID).Error; err == nil && currentJob.Status == JobFailed {
			if currentJob.ErrorCode != "model_timeout" {
				t.Fatalf("error code = %q", currentJob.ErrorCode)
			}
			var currentCount int64
			if err := db.Model(&model.MeetingOrchestrationCurrent{}).Count(&currentCount).Error; err != nil {
				t.Fatal(err)
			}
			if currentCount != 0 {
				t.Fatal("timed out job published a current projection")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("meeting job did not time out")
}

func TestValidateProjectionSourcesRejectsCrossVideoAndStaleGeneration(t *testing.T) {
	videos := []model.Video{{ID: "v1", TranscriptGeneration: "g1"}}
	base := Projection{SchemaVersion: SchemaVersion, TopicClusters: []TopicCluster{{
		ClusterID: "topic-01", Title: "客户项目", BusinessObject: "客户项目",
		SourceVideoIDs: []string{"v1"}, WorkItems: []WorkItem{{
			ID: "item-01", Title: "审批流改造", Status: "in_progress",
			EvidenceRefs: []EvidenceRef{{VideoID: "v1", TranscriptGeneration: "g1", EvidenceID: "e1", StartMs: 0, EndMs: 1000}},
		}},
	}}, TopicRelations: []TopicRelation{}}
	if err := validateProjectionSources(base, videos); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	base.TopicClusters[0].WorkItems[0].EvidenceRefs[0].VideoID = "v2"
	if err := validateProjectionSources(base, videos); err == nil {
		t.Fatal("cross-video evidence was accepted")
	}
	base.TopicClusters[0].WorkItems[0].EvidenceRefs[0] = EvidenceRef{VideoID: "v1", TranscriptGeneration: "stale", EvidenceID: "e1", StartMs: 0, EndMs: 1000}
	if err := validateProjectionSources(base, videos); err == nil {
		t.Fatal("stale transcript generation was accepted")
	}
}
