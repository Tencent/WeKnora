package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const evidenceText = "A transaction commits all its writes atomically. A lease expires after its deadline. A primary key uniquely identifies a row."

type fakeModels struct {
	interfaces.ModelService
	client chat.Chat
}

func (m fakeModels) GetChatModel(context.Context, string) (chat.Chat, error) { return m.client, nil }

type fakeChatBase interface{ chat.Chat }

type fakeChat struct {
	fakeChatBase
	mu        sync.Mutex
	calls     int
	responses []string
	hook      func(int)
	t         *testing.T
}

func (f *fakeChat) Chat(ctx context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	require.True(f.t, types.LLMContentRedacted(ctx))
	deadline, ok := ctx.Deadline()
	require.True(f.t, ok)
	require.LessOrEqual(f.t, time.Until(deadline), 180*time.Second)
	for _, msg := range messages {
		require.NotContains(f.t, msg.Content, "PERSONAL_MEMORY_SECRET")
	}
	if i == 1 {
		require.NotContains(f.t, messages[1].Content, "answer_index")
		require.NotContains(f.t, messages[1].Content, "PRIVATE_EXPLANATION")
	}
	if f.hook != nil {
		f.hook(i)
	}
	if i >= len(f.responses) {
		return nil, errors.New("PRIVATE_PROVIDER_ERROR")
	}
	return &types.ChatResponse{Content: f.responses[i]}, nil
}

type fakeTasks struct {
	tasks []*asynq.Task
	fail  bool
	hook  func(*asynq.Task)
}

func (f *fakeTasks) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if f.hook != nil {
		f.hook(t)
	}
	if f.fail {
		return nil, errors.New("queue unavailable")
	}
	f.tasks = append(f.tasks, t)
	return &asynq.TaskInfo{}, nil
}

type fixture struct {
	db    *gorm.DB
	repo  interfaces.LearningRepository
	svc   interfaces.LearningService
	ctx   context.Context
	scope interfaces.LearningScope
	page  *types.WikiPage
	chunk *types.Chunk
	kb    *types.KnowledgeBase
	model *fakeChat
	tasks *fakeTasks
}

func webContext(tenant uint64, user string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenant)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: tenant, UserID: user})
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: user})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "learning.db")+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=1"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.WikiPage{}, &types.Knowledge{}, &types.Chunk{}))
	migration, err := os.ReadFile("../../../../migrations/sqlite/000014_learning.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	kb := &types.KnowledgeBase{ID: uuid.NewString(), TenantID: 7, Name: "Test", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}, WikiConfig: &types.WikiConfig{SynthesisModelID: "model"}}
	require.NoError(t, db.Create(kb).Error)
	doc := &types.Knowledge{ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: kb.ID, EnableStatus: "enabled", ParseStatus: "completed", CustomMetadata: types.JSON("{}")}
	require.NoError(t, db.Create(doc).Error)
	chunk := &types.Chunk{ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: kb.ID, KnowledgeID: doc.ID, Content: evidenceText, IsEnabled: true, IndexStatus: "ready"}
	require.NoError(t, db.Create(chunk).Error)
	page := &types.WikiPage{ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: kb.ID, Slug: "concept/transactions", Title: "Transactions",
		PageType: "concept", Status: "published", Content: evidenceText, Summary: "Atomic transactions", Version: 1,
		SourceRefs: types.StringArray{doc.ID + "|Source"}, ChunkRefs: types.StringArray{chunk.ID}}
	require.NoError(t, db.Create(page).Error)
	model := &fakeChat{t: t, responses: []string{generationResponse(chunk), `{"answers":[{"index":3,"ambiguous":false},{"index":2,"ambiguous":false},{"index":1,"ambiguous":false}]}`}}
	tasks := &fakeTasks{}
	repo := repository.NewLearningRepository(db)
	f := &fixture{db: db, repo: repo, ctx: webContext(7, "alice"), scope: interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}, page: page, chunk: chunk, kb: kb, model: model, tasks: tasks}
	f.svc = NewService(repo, fakeModels{client: model}, tasks, nil)
	return f
}

func generationResponse(c *types.Chunk) string {
	questions := make([]proposedQuestion, 3)
	zero := 0
	for i := range questions {
		questions[i] = proposedQuestion{Prompt: fmt.Sprintf("Which statement about atomic transactions is supported (%d)?", i),
			Options: []string{"All writes commit together", "Only half commit", "No writes commit", "Keys may duplicate"}, AnswerIndex: &zero,
			Explanation: "PRIVATE_EXPLANATION: the source states atomic commitment.", Evidence: []types.LearningEvidence{{ChunkID: c.ID, KnowledgeID: c.KnowledgeID, Quote: "A transaction commits all its writes atomically."}}}
	}
	b, _ := json.Marshal(map[string]any{"questions": questions})
	return string(b)
}

func (f *fixture) prepare(t *testing.T) *types.LearningQuizView {
	t.Helper()
	_, err := f.svc.SetEnabled(f.ctx, true)
	require.NoError(t, err)
	quiz, err := f.svc.PrepareQuiz(f.ctx, f.page.ID)
	require.NoError(t, err)
	return quiz
}

func (f *fixture) ready(t *testing.T) *types.LearningQuizView {
	t.Helper()
	q := f.prepare(t)
	require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[len(f.tasks.tasks)-1]))
	q, err := f.svc.GetQuiz(f.ctx, q.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", q.Status)
	require.Len(t, q.Questions, 3)
	return q
}

func TestLearningEndToEndPrivacyMasteryAndExport(t *testing.T) {
	f := newFixture(t)
	settings, err := f.svc.GetSettings(f.ctx)
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	_, err = f.svc.Node(f.ctx, f.page.ID)
	require.ErrorIs(t, err, types.ErrLearningDisabled)
	f.tasks.hook = func(task *asynq.Task) {
		var p types.LearningGeneratePayload
		require.NoError(t, json.Unmarshal(task.Payload(), &p))
		var q types.LearningQuiz
		require.NoError(t, f.db.First(&q, "id = ?", p.QuizID).Error)
		require.Equal(t, "pending", q.Status)
	}
	q := f.ready(t)
	require.Equal(t, 2, f.model.calls)
	b, err := json.Marshal(q)
	require.NoError(t, err)
	require.NotContains(t, string(b), "correct_option")
	require.NotContains(t, string(b), "PRIVATE_EXPLANATION")
	exported, err := f.svc.Export(f.ctx, "")
	require.NoError(t, err)
	b, _ = json.Marshal(exported)
	require.NotContains(t, string(b), "PRIVATE_EXPLANATION")
	require.NoError(t, f.svc.RecordView(f.ctx, f.page.ID))
	n, err := f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.True(t, n.Familiar)
	require.Zero(t, n.Mastery.Attempts)
	for i, question := range q.Questions {
		answer := types.LearningAnswer{QuestionID: question.ID, OptionID: "0", AttemptID: fmt.Sprintf("a%d", i)}
		result, err := f.svc.SubmitAnswer(f.ctx, answer)
		require.NoError(t, err)
		require.True(t, result.Correct)
		require.Equal(t, i+1, result.Mastery.Attempts)
		replay, err := f.svc.SubmitAnswer(f.ctx, answer)
		require.NoError(t, err)
		require.Equal(t, result, replay)
		answer.OptionID = "1"
		_, err = f.svc.SubmitAnswer(f.ctx, answer)
		require.ErrorIs(t, err, types.ErrLearningConflict)
	}
	n, err = f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.Equal(t, "mastered", n.Mastery.State)
	view, err := f.svc.GetQuiz(f.ctx, q.ID)
	require.NoError(t, err)
	require.True(t, view.Questions[0].Answered)
	overview, err := f.svc.Overview(f.ctx, f.kb.ID)
	require.NoError(t, err)
	require.Equal(t, 1, overview.Counts.Mastered)
	_, err = f.svc.SetEnabled(f.ctx, false)
	require.NoError(t, err)
	exported, err = f.svc.Export(f.ctx, "")
	require.NoError(t, err)
	require.Len(t, exported.Attempts, 3)
	cleared, err := f.svc.Clear(f.ctx, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), cleared.DeletedAttempts)
	require.Equal(t, int64(1), cleared.DeletedQuizzes)
	for _, table := range []string{"learning_mastery", "learning_quizzes", "learning_questions", "learning_attempts"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
	var profile types.LearningProfile
	require.NoError(t, f.db.First(&profile).Error)
	require.False(t, profile.Enabled)
	require.Greater(t, profile.Epoch, int64(1))
}

func TestLearningStrictIdentityAndIsolation(t *testing.T) {
	f := newFixture(t)
	q := f.ready(t)
	legacy := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	legacy = context.WithValue(legacy, types.UserIDContextKey, "alice")
	contexts := []context.Context{context.Background(), legacy, types.WithExecutionTenant(f.ctx, 8),
		types.WithPrincipal(f.ctx, types.Principal{Type: types.PrincipalWebUser, ID: "bob"}),
		types.WithTenantAPIKeyScope(f.ctx, types.TenantAPIKeyScope{})}
	for _, kind := range []string{types.PrincipalAPITenant, types.PrincipalIMUser, types.PrincipalEmbedVisitor} {
		contexts = append(contexts, types.WithPrincipal(f.ctx, types.Principal{Type: kind, ID: "alice"}))
	}
	for _, ctx := range contexts {
		_, err := f.svc.GetSettings(ctx)
		require.ErrorIs(t, err, types.ErrLearningForbidden)
	}
	for _, ctx := range []context.Context{webContext(7, "bob"), webContext(8, "alice")} {
		_, err := f.svc.SetEnabled(ctx, true)
		require.NoError(t, err)
		_, err = f.svc.GetQuiz(ctx, q.ID)
		require.ErrorIs(t, err, types.ErrLearningNotFound)
		_, err = f.svc.SubmitAnswer(ctx, types.LearningAnswer{QuestionID: q.Questions[0].ID, OptionID: "0", AttemptID: "a"})
		require.ErrorIs(t, err, types.ErrLearningNotFound)
	}
	_, err := f.svc.Node(webContext(8, "alice"), f.page.ID)
	require.ErrorIs(t, err, types.ErrLearningNotFound)
}

func TestLearningRejectMalformedOrUnverifiedModel(t *testing.T) {
	for _, kind := range []string{"malformed", "unknown", "duplicate", "quote", "option", "missing_index", "disagree", "ambiguous", "provider"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			switch kind {
			case "malformed":
				f.model.responses[0] = "PRIVATE_BAD_RESPONSE"
			case "unknown":
				f.model.responses[0] = `{"extra":1,"questions":[]}`
			case "duplicate":
				f.model.responses[0] = `{"questions":[],"questions":[]}`
			case "quote":
				f.model.responses[0] = strings.ReplaceAll(f.model.responses[0], "A transaction commits all its writes atomically.", "Fabricated evidence not in the original source.")
			case "option":
				f.model.responses[0] = strings.ReplaceAll(f.model.responses[0], "Only half commit", "All writes commit together")
			case "missing_index":
				f.model.responses[0] = strings.ReplaceAll(f.model.responses[0], `"answer_index":0,`, "")
			case "disagree":
				f.model.responses[1] = `{"answers":[{"index":0,"ambiguous":false},{"index":0,"ambiguous":false},{"index":0,"ambiguous":false}]}`
			case "ambiguous":
				f.model.responses[1] = strings.ReplaceAll(f.model.responses[1], "false", "true")
			case "provider":
				f.model.responses = nil
			}
			q := f.prepare(t)
			require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[0]))
			q, err := f.svc.GetQuiz(f.ctx, q.ID)
			require.NoError(t, err)
			require.Equal(t, "failed", q.Status)
			require.Empty(t, q.Questions)
			require.NotContains(t, q.ErrorCode, "PRIVATE")
			require.LessOrEqual(t, f.model.calls, 2)
			var count int64
			require.NoError(t, f.db.Model(&types.LearningQuestion{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestLearningSourceFreshnessAtEveryBoundary(t *testing.T) {
	mutations := map[string]func(*fixture){
		"chunk_content": func(f *fixture) {
			require.NoError(t, f.db.Model(f.chunk).Update("content", evidenceText+" Changed").Error)
		},
		"revision": func(f *fixture) { require.NoError(t, f.db.Model(f.chunk).Update("content_revision", 1).Error) },
		"page_content": func(f *fixture) {
			require.NoError(t, f.db.Model(f.page).Update("content", "Changed actual page content").Error)
		},
		"page_version": func(f *fixture) { require.NoError(t, f.db.Model(f.page).Update("version", 2).Error) },
		"membership": func(f *fixture) {
			require.NoError(t, f.db.Model(f.page).Update("chunk_refs", types.StringArray{}).Error)
		},
		"disabled_chunk": func(f *fixture) { require.NoError(t, f.db.Model(f.chunk).Update("is_enabled", false).Error) },
		"moved_document": func(f *fixture) {
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", f.chunk.KnowledgeID).Update("knowledge_base_id", "elsewhere").Error)
		},
		"deleted_kb": func(f *fixture) { require.NoError(t, f.db.Delete(f.kb).Error) },
	}
	for name, mutate := range mutations {
		for _, boundary := range []string{"claim", "publish", "serve", "answer"} {
			t.Run(name+"/"+boundary, func(t *testing.T) {
				f := newFixture(t)
				q := f.prepare(t)
				if boundary == "claim" {
					mutate(f)
				}
				if boundary == "publish" {
					f.model.hook = func(i int) {
						if i == 1 {
							mutate(f)
						}
					}
				}
				require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[0]))
				var questionID string
				if boundary == "serve" || boundary == "answer" {
					ready, err := f.svc.GetQuiz(f.ctx, q.ID)
					require.NoError(t, err)
					questionID = ready.Questions[0].ID
					mutate(f)
				}
				if boundary == "answer" {
					_, err := f.svc.SubmitAnswer(f.ctx, types.LearningAnswer{QuestionID: questionID, OptionID: "0", AttemptID: "a"})
					require.ErrorIs(t, err, types.ErrLearningStale)
				}
				q, err := f.svc.GetQuiz(f.ctx, q.ID)
				require.NoError(t, err)
				require.Equal(t, "stale", q.Status)
				require.Empty(t, q.Questions)
				if boundary == "claim" {
					require.Zero(t, f.model.calls)
				}
				var count int64
				require.NoError(t, f.db.Model(&types.LearningAttempt{}).Count(&count).Error)
				require.Zero(t, count)
			})
		}
	}
}

func TestLearningConcurrentFirstAnswerAndRegenerationFingerprint(t *testing.T) {
	f := newFixture(t)
	q := f.ready(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.svc.SubmitAnswer(f.ctx, types.LearningAnswer{QuestionID: q.Questions[0].ID, OptionID: "0", AttemptID: fmt.Sprintf("race%d", i)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, types.ErrLearningConflict)
		}
	}
	require.Equal(t, 1, successes)
	for i := 1; i < 3; i++ {
		_, err := f.svc.SubmitAnswer(f.ctx, types.LearningAnswer{QuestionID: q.Questions[i].ID, OptionID: "0", AttemptID: fmt.Sprint(i)})
		require.NoError(t, err)
	}
	f.model.responses = append(f.model.responses, f.model.responses...)
	newQuiz, err := f.svc.PrepareQuiz(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.NotEqual(t, q.ID, newQuiz.ID)
	require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[len(f.tasks.tasks)-1]))
	newQuiz, err = f.svc.GetQuiz(f.ctx, newQuiz.ID)
	require.NoError(t, err)
	a, err := f.svc.SubmitAnswer(f.ctx, types.LearningAnswer{QuestionID: newQuiz.Questions[0].ID, OptionID: "0", AttemptID: "repeat"})
	require.NoError(t, err)
	require.Equal(t, 3, a.Mastery.Attempts)
	var count int64
	require.NoError(t, f.db.Model(&types.LearningAttempt{}).Where("credited = ?", true).Count(&count).Error)
	require.Equal(t, int64(3), count)
}

func TestLearningClearSuppressesOldWorkerAndQueueRecovery(t *testing.T) {
	f := newFixture(t)
	f.tasks.fail = true
	q := f.prepare(t)
	require.Equal(t, "pending", q.Status)
	require.Empty(t, f.tasks.tasks)
	f.tasks.fail = false
	require.NoError(t, f.svc.Recover(context.Background()))
	require.Len(t, f.tasks.tasks, 1)
	f.model.hook = func(i int) {
		if i == 1 {
			_, err := f.svc.Clear(f.ctx, "")
			require.NoError(t, err)
			_, err = f.svc.SetEnabled(f.ctx, true)
			require.NoError(t, err)
		}
	}
	require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[0]))
	require.NoError(t, f.svc.Handle(context.Background(), f.tasks.tasks[0]))
	require.Equal(t, 2, f.model.calls)
	var count int64
	require.NoError(t, f.db.Model(&types.LearningQuiz{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestLearningRecoveryPurgesOrphansWithoutResurrection(t *testing.T) {
	f := newFixture(t)
	f.prepare(t)
	var payload types.LearningGeneratePayload
	require.NoError(t, json.Unmarshal(f.tasks.tasks[0].Payload(), &payload))
	claim, err := f.repo.Claim(context.Background(), payload)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.NoError(t, f.svc.RecordView(f.ctx, f.page.ID))
	require.NoError(t, f.db.Delete(f.page).Error)
	require.NoError(t, f.svc.Recover(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	questions, code := generate(types.WithLLMContentRedacted(ctx), f.model, &claim.Source)
	require.Empty(t, code)
	require.ErrorIs(t, f.repo.Publish(context.Background(), claim, questions), types.ErrLearningStale)
	for _, table := range []string{"learning_quizzes", "learning_questions", "learning_mastery"} {
		var n int64
		require.NoError(t, f.db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
}
