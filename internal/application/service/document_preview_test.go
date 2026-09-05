package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type previewQueue struct{ err error }

func (q previewQueue) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return nil, q.err
}

type previewReader struct {
	interfaces.DocumentReader
	calls  atomic.Int32
	before func()
	fail   bool
	busy   bool
	output []byte
}

func (r *previewReader) NormalizeLegacyDoc(_ context.Context, src io.Reader) ([]byte, error) {
	r.calls.Add(1)
	_, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	if r.before != nil {
		r.before()
	}
	if r.fail {
		return nil, errors.New("conversion failed")
	}
	if r.busy {
		return nil, interfaces.ErrLegacyDocPreviewBusy
	}
	return r.output, nil
}

func previewFixture(
	t *testing.T,
) (*documentPreviewService, *gorm.DB, *previewReader, *types.Knowledge) {
	t.Helper()
	var db *gorm.DB
	var err error
	dsn := os.Getenv("WEKNORA_PREVIEW_TEST_DSN")
	if dsn != "" {
		admin, openErr := gorm.Open(
			postgres.Open(dsn),
			&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
		)
		require.NoError(t, openErr)
		schema := "preview_" + hex.EncodeToString([]byte(uuid.NewString()))
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		db, err = gorm.Open(
			postgres.Open(dsn+" search_path="+schema),
			&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
		)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			adminConn, connErr := admin.DB()
			require.NoError(t, connErr)
			require.NoError(t, adminConn.Close())
		})
	} else {
		db, err = gorm.Open(
			sqlite.Open(filepath.Join(t.TempDir(), "preview.db")),
			&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
		)
	}
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	if dsn == "" {
		conn.SetMaxOpenConns(1)
	} else {
		conn.SetMaxOpenConns(8)
	}
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{},
		&types.TaskPendingOp{},
		&types.StoredResource{},
		&types.ResourceBinding{},
	))
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledge_bases(
id VARCHAR(36) PRIMARY KEY,tenant_id INTEGER,deleted_at TIMESTAMP)`,
	).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id,tenant_id) VALUES ('kb1',42)`).Error)
	catalog := NewResourceCatalog(repository.NewResourceRepository(db))
	file := filesvc.NewResourceCatalogFileService(filesvc.NewLocalFileService(t.TempDir(), ""), catalog)
	original := []byte("synthetic original DOC")
	path, err := file.SaveBytes(context.Background(), original, 42, "source.doc", false)
	require.NoError(t, err)
	hash := md5.Sum(original)
	k := &types.Knowledge{
		ID: "knowledge-1", TenantID: 42, KnowledgeBaseID: "kb1", FileName: "source.doc",
		FilePath: path, FileHash: hex.EncodeToString(hash[:]),
		ParseStatus: types.ParseStatusFailed, StorageSize: 123,
	}
	require.NoError(t, repository.NewKnowledgeRepository(db).CreateKnowledge(context.Background(), k))
	docx, err := os.ReadFile("../../../docreader/tests/fixtures/legacy_preview.docx")
	require.NoError(t, err)
	reader := &previewReader{output: docx}
	svc := NewDocumentPreviewService(
		repository.NewDocumentPreviewRepository(db), nil, nil, catalog, nil, file, reader,
		previewQueue{err: errors.New("redis unavailable")},
	).(*documentPreviewService)
	return svc, db, reader, k
}

func previewJob(id int64) *asynq.Task {
	b, _ := json.Marshal(map[string]int64{"id": id})
	return asynq.NewTask(types.TypeDocumentPreview, b)
}

func runPreview(t *testing.T, s *documentPreviewService, k *types.Knowledge) *types.TaskPendingOp {
	t.Helper()
	op, err := s.repo.Ensure(context.Background(), k.TenantID, k.ID, false)
	require.NoError(t, err)
	require.NoError(t, s.Handle(context.Background(), previewJob(op.ID)))
	return op
}

func expirePreviewArtifacts(t *testing.T, db *gorm.DB) {
	t.Helper()
	var ops []types.TaskPendingOp
	require.NoError(t, db.Where("op = ?", "artifact").Find(&ops).Error)
	for _, op := range ops {
		var a types.DocumentPreviewArtifact
		require.NoError(t, json.Unmarshal(op.Payload, &a))
		a.NotBefore = time.Now().UTC().Add(-time.Hour)
		a.NextCheck = a.NotBefore
		b, err := json.Marshal(a)
		require.NoError(t, err)
		require.NoError(t, db.Model(&op).Update("payload", json.RawMessage(b)).Error)
	}
}

func cleanPreviewArtifacts(t *testing.T, s *documentPreviewService, db *gorm.DB) {
	t.Helper()
	expirePreviewArtifacts(t, db)
	var ops []types.TaskPendingOp
	require.NoError(t, db.Where("op = ?", "artifact").Find(&ops).Error)
	for _, op := range ops {
		require.NoError(t, s.Handle(context.Background(), previewJob(op.ID)))
	}
}

func TestDocumentPreviewPersistsAcrossRepeatedOpensAndRestart(t *testing.T) {
	s, db, reader, k := previewFixture(t)
	ctx := context.Background()
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("op = ?", "state").Count(&count).Error)
	require.Equal(t, int64(1), count, "upload must durably seed work even before enqueue")
	pending, err := s.Get(ctx, 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Zero(t, reader.calls.Load(), "HTTP must not convert")
	runPreview(t, s, k)
	for i := 0; i < 3; i++ {
		res, err := s.Get(ctx, 42, k.ID, false)
		require.NoError(t, err)
		require.Equal(t, "ready", res.Status)
		b, err := io.ReadAll(res.Content)
		require.NoError(t, err)
		require.NoError(t, res.Content.Close())
		require.Equal(t, reader.output, b)
	}
	restarted := NewDocumentPreviewService(
		repository.NewDocumentPreviewRepository(db), nil, nil, s.catalog, nil, s.file, reader, s.queue,
	).(*documentPreviewService)
	res, err := restarted.Get(ctx, 42, k.ID, false)
	require.NoError(t, err)
	require.NoError(t, res.Content.Close())
	require.Equal(t, int32(1), reader.calls.Load())
	cleanPreviewArtifacts(t, s, db)
	require.NoError(t, db.Model(&types.ResourceBinding{}).
		Where("relation = ?", types.ResourceRelationPreviewFile).
		Count(&count).Error)
	require.Equal(t, int64(1), count, "ready artifact must remain")
	original, err := s.file.GetFile(ctx, k.FilePath)
	require.NoError(t, err)
	data, _ := io.ReadAll(original)
	require.NoError(t, original.Close())
	require.Equal(t, "synthetic original DOC", string(data))
	var current types.Knowledge
	require.NoError(t, db.First(&current, "id = ?", k.ID).Error)
	require.Equal(t, int64(123), current.StorageSize)
	require.Equal(t, types.ParseStatusFailed, current.ParseStatus, "preview independent from parsing")
}

func TestDocumentPreviewConcurrentRequestsAndDuplicateTasks(t *testing.T) {
	s, db, r, k := previewFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
			if err == nil {
				err = s.Handle(context.Background(), previewJob(op.ID))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), r.calls.Load())
	var count int64
	require.NoError(t, db.Model(&types.ResourceBinding{}).
		Where("relation = ?", types.ResourceRelationPreviewFile).
		Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestDocumentPreviewDeleteOrVersionChangeDuringConversion(t *testing.T) {
	for _, mode := range []string{"delete", "deleting", "kb-delete", "version"} {
		t.Run(mode, func(t *testing.T) {
			s, db, r, k := previewFixture(t)
			r.before = func() {
				switch mode {
				case "delete":
					require.NoError(t, db.Delete(k).Error)
				case "deleting":
					require.NoError(t, db.Model(k).Update("parse_status", types.ParseStatusDeleting).Error)
				case "kb-delete":
					require.NoError(t, db.Exec(
						"UPDATE knowledge_bases SET deleted_at = ? WHERE id = ?", time.Now(), "kb1",
					).Error)
				case "version":
					require.NoError(t, db.Model(k).Update("file_hash", "changed").Error)
				}
			}
			runPreview(t, s, k)
			cleanPreviewArtifacts(t, s, db)
			var count int64
			require.NoError(t, db.Model(&types.ResourceBinding{}).
				Where("relation = ?", types.ResourceRelationPreviewFile).
				Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("op = ?", "artifact").Count(&count).Error)
			require.Zero(t, count, "cleanup must survive owner loss")
			var resources []types.StoredResource
			require.NoError(t, db.Unscoped().Where("original_name = ?", "preview.docx").Find(&resources).Error)
			require.Len(t, resources, 1)
			_, err := s.file.GetFile(context.Background(), resources[0].PhysicalPath)
			require.Error(t, err)
		})
	}
}

func TestDocumentPreviewCleanupRetainsOtherOwners(t *testing.T) {
	s, db, _, k := previewFixture(t)
	runPreview(t, s, k)
	op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	state, err := repository.DecodeDocumentPreview(op)
	require.NoError(t, err)
	require.NoError(t, s.catalog.Bind(
		context.Background(), state.ResourceRef,
		types.ResourceOwnerMessage, "message-1", types.ResourceRelationAttachment,
	))
	require.NoError(t, db.Delete(k).Error)
	cleanPreviewArtifacts(t, s, db)
	file, err := s.file.GetFile(context.Background(), state.ResourceRef)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	var bindings []types.ResourceBinding
	require.NoError(t, db.Find(&bindings).Error)
	require.Len(t, bindings, 1)
	require.Equal(t, "message-1", bindings[0].OwnerID)
}

func TestDocumentPreviewFailedAttemptsBoundedAndTenantIsolated(t *testing.T) {
	s, db, r, k := previewFixture(t)
	r.fail = true
	_, err := s.Get(context.Background(), 43, k.ID, false)
	require.ErrorIs(t, err, repository.ErrPreviewGone)
	for i := 0; i < 3; i++ {
		op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
		require.NoError(t, err)
		state, err := repository.DecodeDocumentPreview(op)
		require.NoError(t, err)
		state.NextAttempt = time.Time{}
		b, _ := json.Marshal(state)
		require.NoError(t, db.Model(op).Update("payload", json.RawMessage(b)).Error)
		require.NoError(t, s.Handle(context.Background(), previewJob(op.ID)))
	}
	result, err := s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	runPreview(t, s, k)
	require.Equal(t, int32(3), r.calls.Load())
}

func TestDocumentPreviewOldFileHashAndSourceVersionInvalidation(t *testing.T) {
	s, db, r, k := previewFixture(t)
	require.NoError(t, db.Model(k).Update("file_hash", "").Error)
	runPreview(t, s, k)
	var current types.Knowledge
	require.NoError(t, db.First(&current, "id = ?", k.ID).Error)
	require.NotEmpty(t, current.FileHash)
	path, err := s.file.SaveBytes(context.Background(), []byte("replacement"), 42, "new.doc", false)
	require.NoError(t, err)
	hash := md5.Sum([]byte("replacement"))
	require.NoError(t, db.Model(k).Updates(map[string]any{
		"file_path": path, "file_hash": hex.EncodeToString(hash[:]),
	}).Error)
	pending, err := s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	runPreview(t, s, k)
	cleanPreviewArtifacts(t, s, db)
	require.Equal(t, int32(2), r.calls.Load())
	var count int64
	require.NoError(t, db.Model(&types.ResourceBinding{}).
		Where("relation = ?", types.ResourceRelationPreviewFile).
		Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestDocumentPreviewCrashAfterIntentBeforeRegistration(t *testing.T) {
	s, db, _, k := previewFixture(t)
	op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	claimed, err := s.repo.Claim(context.Background(), op)
	require.NoError(t, err)
	require.True(t, claimed)
	// The callback commits before bytes are written. Simulate a crash after the
	// physical write, before ResourceCatalog.Register receives control.
	var physical string
	local := filesvc.NewLocalFileService(t.TempDir(), "")
	ctx := interfaces.WithFileWriteIntent(
		context.Background(),
		func(ctx context.Context, path string) error {
			physical = path
			return s.repo.Track(ctx, op, path)
		},
	)
	_, err = local.SaveBytes(ctx, bytes.Repeat([]byte("x"), 32), 42, "preview.docx", false)
	require.NoError(t, err)
	old := time.Now().UTC().Add(-2 * types.DocumentPreviewLease)
	require.NoError(t, db.Model(op).Update("claimed_at", old).Error)
	s.file = local
	cleanPreviewArtifacts(t, s, db)
	_, err = local.GetFile(context.Background(), physical)
	require.Error(t, err)
}

func TestDocumentPreviewReadyRegistryLossAndCrashRetryCap(t *testing.T) {
	s, db, r, k := previewFixture(t)
	runPreview(t, s, k)
	op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	state, err := repository.DecodeDocumentPreview(op)
	require.NoError(t, err)
	require.NoError(t, s.catalog.MarkDeleted(context.Background(), state.ResourceRef))
	got, err := s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status)
	op, err = s.repo.Ensure(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	old := time.Now().UTC().Add(-2 * types.DocumentPreviewLease)
	require.NoError(t, db.Model(op).Updates(map[string]any{"fail_count": 3, "claimed_at": old}).Error)
	require.NoError(t, s.Handle(context.Background(), previewJob(op.ID)))
	got, err = s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status)
	require.Equal(t, int32(1), r.calls.Load(), "crash-retry cap must prevent a fourth attempt")
}

func TestDocumentPreviewMissingBytesRegenerate(t *testing.T) {
	s, _, r, k := previewFixture(t)
	runPreview(t, s, k)
	op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	state, err := repository.DecodeDocumentPreview(op)
	require.NoError(t, err)
	resource, err := s.catalog.Resolve(context.Background(), state.ResourceRef)
	require.NoError(t, err)
	require.NoError(t, s.file.DeleteFile(context.Background(), resource.PhysicalPath))
	res, err := s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	runPreview(t, s, k)
	res, err = s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.NoError(t, res.Content.Close())
	require.Equal(t, int32(2), r.calls.Load())
}

func TestDocumentPreviewRecoveryOnlyWakesDueWork(t *testing.T) {
	s, db, _, k := previewFixture(t)
	ctx := context.Background()
	due, err := s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "state", due[0].Op)
	runPreview(t, s, k)
	due, err = s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, due, "ready state and fresh artifact must not churn every scan")
	expirePreviewArtifacts(t, db)
	due, err = s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "artifact", due[0].Op)
	require.NoError(t, s.Handle(ctx, previewJob(due[0].ID)))
	due, err = s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, due, "valid artifact should be deferred for one hour")
	require.NoError(t, db.Delete(k).Error)
	due, err = s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Len(t, due, 2, "owner deletion bypasses hourly wait after write lease")
	for _, op := range due {
		require.NoError(t, s.Handle(ctx, previewJob(op.ID)))
	}
	due, err = s.repo.List(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, due)
}

func TestDocumentPreviewBusyDoesNotUseFailureBudget(t *testing.T) {
	s, db, r, k := previewFixture(t)
	r.busy = true
	for i := 0; i < 6; i++ {
		op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
		require.NoError(t, err)
		state, err := repository.DecodeDocumentPreview(op)
		require.NoError(t, err)
		state.NextAttempt = time.Time{}
		b, _ := json.Marshal(state)
		require.NoError(t, db.Model(op).Update("payload", json.RawMessage(b)).Error)
		runPreview(t, s, k)
		op, err = s.repo.Ensure(context.Background(), 42, k.ID, false)
		require.NoError(t, err)
		require.Zero(t, op.FailCount)
		state, err = repository.DecodeDocumentPreview(op)
		require.NoError(t, err)
		require.Equal(t, "pending", state.Status)
		due, err := s.repo.List(context.Background(), 0)
		require.NoError(t, err)
		require.Empty(t, due, "busy work respects retry backoff")
	}
}

type lazyPreviewError struct{ err error }

func (r lazyPreviewError) Read([]byte) (int, error) { return 0, r.err }

type previewReadOverride struct {
	interfaces.FileService
	ref  string
	read func() io.ReadCloser
}

func (s previewReadOverride) GetFile(ctx context.Context, path string) (io.ReadCloser, error) {
	if path == s.ref {
		return s.read(), nil
	}
	return s.FileService.GetFile(ctx, path)
}

func TestDocumentPreviewLazyCloudReadAndCorruptCache(t *testing.T) {
	for _, mode := range []string{"missing", "denied", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, k := previewFixture(t)
			runPreview(t, s, k)
			op, err := s.repo.Ensure(context.Background(), 42, k.ID, false)
			require.NoError(t, err)
			state, err := repository.DecodeDocumentPreview(op)
			require.NoError(t, err)
			s.file = previewReadOverride{FileService: s.file, ref: state.ResourceRef, read: func() io.ReadCloser {
				if mode == "corrupt" {
					return io.NopCloser(bytes.NewBufferString("broken zip"))
				}
				code := "NoSuchKey"
				if mode == "denied" {
					code = "AccessDenied"
				}
				return io.NopCloser(lazyPreviewError{minio.ErrorResponse{Code: code}})
			}}
			res, err := s.Get(context.Background(), 42, k.ID, false)
			if mode == "denied" {
				require.Error(t, err)
				op, err = s.repo.Ensure(context.Background(), 42, k.ID, false)
				require.NoError(t, err)
				state, err = repository.DecodeDocumentPreview(op)
				require.NoError(t, err)
				require.Equal(t, "ready", state.Status)
			} else {
				require.NoError(t, err)
				require.Equal(t, "pending", res.Status)
				require.Nil(t, res.Content, "must not commit200 before read succeeds")
			}
		})
	}
}

func TestDocumentPreviewIntentFailurePreventsWrite(t *testing.T) {
	s, db, _, k := previewFixture(t)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(
		"reject_preview_intent",
		func(tx *gorm.DB) {
			if op, ok := tx.Statement.Dest.(*types.TaskPendingOp); ok && op.Op == "artifact" {
				_ = tx.AddError(errors.New("intent database unavailable"))
			}
		},
	))
	runPreview(t, s, k)
	var count int64
	require.NoError(t, db.Model(&types.StoredResource{}).Where("original_name = ?", "preview.docx").Count(&count).Error)
	require.Zero(t, count)
}

type recordingPreviewQueue struct{ jobs chan int64 }

func (q recordingPreviewQueue) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return nil, err
	}
	q.jobs <- p.ID
	return &asynq.TaskInfo{}, nil
}

func TestDocumentPreviewRecoveryRearmsLostQueueWakeup(t *testing.T) {
	s, _, _, k := previewFixture(t)
	jobs := make(chan int64, 8)
	s.queue = recordingPreviewQueue{jobs: jobs}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	var id int64
	select {
	case id = <-jobs:
	case <-time.After(time.Second):
		t.Fatal("durable upload was not recovered")
	}
	cancel()
	<-done
	require.NoError(t, s.Handle(context.Background(), previewJob(id)))
	res, err := s.Get(context.Background(), 42, k.ID, false)
	require.NoError(t, err)
	require.Equal(t, "ready", res.Status)
	require.NoError(t, res.Content.Close())
}
