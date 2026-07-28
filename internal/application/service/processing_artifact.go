package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	ProcessingArtifactInlineLimit           = 1 << 20
	ProcessingArtifactMaxPayload            = 64 << 20
	processingArtifactPutAttempts           = 3
	processingArtifactObjectReferencePrefix = "processing-artifact:v1:"
)

var errProcessingArtifactObjectNotOwned = errors.New("processing artifact object is not authoritatively owned")

type processingArtifactPurgeFailure struct {
	kind string
	err  error
}

func (e *processingArtifactPurgeFailure) Error() string {
	return e.err.Error()
}

func (e *processingArtifactPurgeFailure) Unwrap() error {
	return e.err
}

func (e *processingArtifactPurgeFailure) ProcessingArtifactFailureKind() string {
	return e.kind
}

func newProcessingArtifactPurgeFailure(kind string, err error) error {
	return &processingArtifactPurgeFailure{kind: kind, err: err}
}

type processingArtifactStore struct {
	repository       interfaces.ProcessingArtifactRepository
	tenantRepository interfaces.TenantRepository
	fileService      interfaces.FileService
	newFileService   processingArtifactFileServiceFactory
	storageResolver  interfaces.StorageBackendResolver
	maxPayloadBytes  int
	counters         interfaces.ProcessingArtifactCounterRegistry
}

type processingArtifactBatchRepository interface {
	GetMany(ctx context.Context, keys []types.ProcessingArtifactKey) (
		map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error,
	)
	PutManyIfAbsent(ctx context.Context, artifacts []*types.ProcessingArtifact) error
}

type processingArtifactWriteProgress struct {
	durable  bool
	complete bool
}

type processingArtifactFileServiceFactory func(
	provider string,
	storageConfig *types.StorageEngineConfig,
	localBaseDir string,
) (interfaces.FileService, string, error)

type processingArtifactHistoricalStorageResolver interface {
	ResolveHistoricalFileService(
		ctx context.Context,
		tenant *types.Tenant,
		backendID, localBaseDir string,
	) (interfaces.FileService, string, error)
}

type processingArtifactFileServiceResolver struct {
	service   interfaces.FileService
	provider  string
	backendID string
}

type processingArtifactPurgeResolverKey struct {
	tenantID  uint64
	provider  string
	backendID string
}

type processingArtifactPurgeResolverResult struct {
	resolver processingArtifactFileServiceResolver
	err      error
}

func NewProcessingArtifactStore(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
) interfaces.ProcessingArtifactStore {
	return NewProcessingArtifactStoreWithMaxPayloadAndCounterRegistry(
		repository, tenantRepository, fileService, ProcessingArtifactMaxPayload, NewProcessingArtifactCounterRegistry(),
	)
}

func NewProcessingArtifactStoreWithMaxPayload(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	maxPayloadBytes int,
) interfaces.ProcessingArtifactStore {
	return NewProcessingArtifactStoreWithMaxPayloadAndCounterRegistry(
		repository, tenantRepository, fileService, maxPayloadBytes, NewProcessingArtifactCounterRegistry(),
	)
}

func NewProcessingArtifactStoreWithMaxPayloadAndCounterRegistry(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	maxPayloadBytes int,
	counters interfaces.ProcessingArtifactCounterRegistry,
) interfaces.ProcessingArtifactStore {
	return newProcessingArtifactStoreWithDependencies(
		repository,
		tenantRepository,
		fileService,
		filesvc.NewFileServiceFromStorageConfig,
		nil,
		maxPayloadBytes,
		counters,
	)
}

func NewProcessingArtifactStoreWithDependencies(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	maxPayloadBytes int,
	counters interfaces.ProcessingArtifactCounterRegistry,
) interfaces.ProcessingArtifactStore {
	return newProcessingArtifactStoreWithDependencies(
		repository,
		tenantRepository,
		fileService,
		filesvc.NewFileServiceFromStorageConfig,
		storageResolver,
		maxPayloadBytes,
		counters,
	)
}

func newProcessingArtifactStore(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	newFileService processingArtifactFileServiceFactory,
	maxPayloadBytes ...int,
) *processingArtifactStore {
	maxPayload := ProcessingArtifactMaxPayload
	if len(maxPayloadBytes) > 0 && maxPayloadBytes[0] > 0 {
		maxPayload = maxPayloadBytes[0]
	}
	return newProcessingArtifactStoreWithCounterRegistry(
		repository, tenantRepository, fileService, newFileService, maxPayload, NewProcessingArtifactCounterRegistry(),
	)
}

func newProcessingArtifactStoreWithCounterRegistry(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	newFileService processingArtifactFileServiceFactory,
	maxPayloadBytes int,
	counters interfaces.ProcessingArtifactCounterRegistry,
) *processingArtifactStore {
	return newProcessingArtifactStoreWithDependencies(
		repository, tenantRepository, fileService, newFileService, nil, maxPayloadBytes, counters,
	)
}

func newProcessingArtifactStoreWithDependencies(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	newFileService processingArtifactFileServiceFactory,
	storageResolver interfaces.StorageBackendResolver,
	maxPayloadBytes int,
	counters interfaces.ProcessingArtifactCounterRegistry,
) *processingArtifactStore {
	maxPayload := ProcessingArtifactMaxPayload
	if maxPayloadBytes > 0 {
		maxPayload = maxPayloadBytes
	}
	if counters == nil {
		counters = NewProcessingArtifactCounterRegistry()
	}
	return &processingArtifactStore{
		repository:       repository,
		tenantRepository: tenantRepository,
		fileService:      fileService,
		newFileService:   newFileService,
		storageResolver:  storageResolver,
		maxPayloadBytes:  maxPayload,
		counters:         counters,
	}
}

func (s *processingArtifactStore) Get(
	ctx context.Context,
	key types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	value, hit, err := s.get(ctx, key)
	s.recordLookup(key.Stage, hit, err)
	return value, hit, err
}

func (s *processingArtifactStore) get(
	ctx context.Context,
	key types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	artifact, err := s.repository.Get(ctx, key)
	if errors.Is(err, types.ErrProcessingArtifactNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get processing artifact manifest: %w", err)
	}
	return s.readArtifact(ctx, key, artifact)
}

func (s *processingArtifactStore) GetMany(
	ctx context.Context,
	keys []types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey][]byte, error) {
	result := make(map[types.ProcessingArtifactKey][]byte, len(keys))
	batchRepository, ok := s.repository.(processingArtifactBatchRepository)
	if !ok {
		for index, key := range keys {
			value, hit, err := s.get(ctx, key)
			if err != nil {
				s.recordLookupErrors(keys[index:])
				return nil, err
			}
			s.recordLookup(key.Stage, hit, nil)
			if hit {
				result[key] = value
			}
		}
		return result, nil
	}

	artifacts, err := batchRepository.GetMany(ctx, keys)
	if err != nil {
		s.recordLookupErrors(keys)
		return nil, fmt.Errorf("get processing artifact manifests: %w", err)
	}
	for index, key := range keys {
		artifact, ok := artifacts[key]
		if !ok {
			s.recordLookup(key.Stage, false, nil)
			continue
		}
		value, hit, err := s.readArtifact(ctx, key, artifact)
		if err != nil {
			s.recordLookupErrors(keys[index:])
			return nil, err
		}
		s.recordLookup(key.Stage, hit, nil)
		if hit {
			result[key] = value
		}
	}
	return result, nil
}

func (s *processingArtifactStore) PutIfAbsent(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	canonical, created, err := s.putIfAbsent(ctx, key, value)
	s.recordWrite(key.Stage, err)
	return canonical, created, err
}

func (s *processingArtifactStore) putIfAbsent(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	if err := s.validatePayloadSize(value); err != nil {
		return nil, false, err
	}
	candidate := cloneProcessingArtifactValue(value)
	hash := processingArtifactSHA256(candidate)

	for attempt := 0; attempt < processingArtifactPutAttempts; attempt++ {
		artifact := &types.ProcessingArtifact{
			TenantID:         key.TenantID,
			Stage:            key.Stage,
			KeyVersion:       key.KeyVersion,
			InputFingerprint: key.InputFingerprint,
			ContentSHA256:    hash,
			SizeBytes:        int64(len(candidate)),
		}

		var candidateResolver processingArtifactFileServiceResolver
		if len(candidate) <= ProcessingArtifactInlineLimit {
			artifact.Payload = cloneProcessingArtifactValue(candidate)
		} else {
			var err error
			candidateResolver, err = s.resolveFileService(ctx, key.TenantID, "", "")
			if err != nil {
				return nil, false, err
			}
			artifact.ObjectPath, err = candidateResolver.service.SaveBytes(
				ctx,
				candidate,
				key.TenantID,
				"processing-artifact-"+hash+".bin",
				false,
			)
			if err != nil {
				return nil, false, fmt.Errorf("save processing artifact object: %w", err)
			}
			if artifact.ObjectPath == "" {
				return nil, false, errors.New("save processing artifact object returned an empty path")
			}
			objectPath := filesvc.CanonicalStoredPath(candidateResolver.service, artifact.ObjectPath)
			artifact.ObjectPath = processingArtifactObjectReference(objectPath)
		}

		created, err := s.repository.PutIfAbsent(ctx, artifact)
		if err != nil {
			return nil, false, fmt.Errorf("put processing artifact manifest: %w", err)
		}
		if created {
			return cloneProcessingArtifactValue(candidate), true, nil
		}

		winner, err := s.repository.Get(ctx, key)
		if errors.Is(err, types.ErrProcessingArtifactNotFound) {
			s.deleteCandidate(ctx, artifact.ObjectPath, candidateResolver)
			continue
		}
		if err != nil {
			s.deleteCandidate(ctx, artifact.ObjectPath, candidateResolver)
			return nil, false, fmt.Errorf("get winning processing artifact manifest: %w", err)
		}
		if artifact.ObjectPath != "" {
			if winner == nil {
				s.deleteCandidate(ctx, artifact.ObjectPath, candidateResolver)
				return nil, false, errors.New("processing artifact repository returned a nil winner")
			}
			// Equality is only observable after the insert loses; detecting it
			// earlier would require a cross-call path registry in FileService.
			if artifact.ObjectPath == winner.ObjectPath {
				return nil, false, errors.New("FileService SaveBytes path uniqueness contract violated")
			}
			s.deleteCandidate(ctx, artifact.ObjectPath, candidateResolver)
		}

		canonical, hit, err := s.readArtifact(ctx, key, winner)
		if err != nil {
			return nil, false, err
		}
		if hit {
			return canonical, false, nil
		}
	}

	return nil, false, fmt.Errorf(
		"processing artifact contention did not converge after %d attempts",
		processingArtifactPutAttempts,
	)
}

func (s *processingArtifactStore) PutManyIfAbsent(
	ctx context.Context,
	values map[types.ProcessingArtifactKey][]byte,
) (map[types.ProcessingArtifactKey][]byte, error) {
	canonical, progress, err := s.putManyIfAbsent(ctx, values)
	s.recordBatchWrites(values, progress)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func (s *processingArtifactStore) putManyIfAbsent(
	ctx context.Context,
	values map[types.ProcessingArtifactKey][]byte,
) (map[types.ProcessingArtifactKey][]byte, map[types.ProcessingArtifactKey]processingArtifactWriteProgress, error) {
	progress := make(map[types.ProcessingArtifactKey]processingArtifactWriteProgress, len(values))
	for _, value := range values {
		if err := s.validatePayloadSize(value); err != nil {
			return nil, progress, err
		}
	}
	result := make(map[types.ProcessingArtifactKey][]byte, len(values))
	batchRepository, ok := s.repository.(processingArtifactBatchRepository)
	if !ok {
		return s.putManySequential(ctx, values, progress)
	}

	inlineValues := make(map[types.ProcessingArtifactKey][]byte, len(values))
	artifacts := make([]*types.ProcessingArtifact, 0, len(values))
	for key, value := range values {
		if len(value) > ProcessingArtifactInlineLimit {
			canonical, _, err := s.putIfAbsent(ctx, key, value)
			if err != nil {
				return nil, progress, err
			}
			result[key] = canonical
			progress[key] = processingArtifactWriteProgress{durable: true, complete: true}
			continue
		}

		candidate := cloneProcessingArtifactValue(value)
		inlineValues[key] = candidate
		artifacts = append(artifacts, &types.ProcessingArtifact{
			TenantID:         key.TenantID,
			Stage:            key.Stage,
			KeyVersion:       key.KeyVersion,
			InputFingerprint: key.InputFingerprint,
			Payload:          cloneProcessingArtifactValue(candidate),
			ContentSHA256:    processingArtifactSHA256(candidate),
			SizeBytes:        int64(len(candidate)),
		})
	}
	if len(artifacts) == 0 {
		return result, progress, nil
	}
	if err := batchRepository.PutManyIfAbsent(ctx, artifacts); err != nil {
		return nil, progress, fmt.Errorf("put processing artifact manifests: %w", err)
	}
	for key := range inlineValues {
		progress[key] = processingArtifactWriteProgress{durable: true}
	}

	keys := make([]types.ProcessingArtifactKey, 0, len(inlineValues))
	for key := range inlineValues {
		keys = append(keys, key)
	}
	winners, err := batchRepository.GetMany(ctx, keys)
	if err != nil {
		return nil, progress, fmt.Errorf("get winning processing artifact manifests: %w", err)
	}
	for key, candidate := range inlineValues {
		winner, ok := winners[key]
		if ok {
			canonical, hit, readErr := s.readArtifact(ctx, key, winner)
			if readErr != nil {
				return nil, progress, readErr
			}
			if hit {
				result[key] = canonical
				progress[key] = processingArtifactWriteProgress{durable: true, complete: true}
				continue
			}
		}
		canonical, _, putErr := s.putIfAbsent(ctx, key, candidate)
		if putErr != nil {
			return nil, progress, putErr
		}
		result[key] = canonical
		progress[key] = processingArtifactWriteProgress{durable: true, complete: true}
	}
	return result, progress, nil
}

func (s *processingArtifactStore) Invalidate(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	observed []byte,
) error {
	evicted, err := s.invalidate(ctx, key, observed)
	if err != nil {
		s.counters.Record(key.Stage, "error")
	} else if evicted {
		s.counters.Record(key.Stage, "evicted")
	}
	return err
}

func (s *processingArtifactStore) invalidate(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	observed []byte,
) (bool, error) {
	artifact, err := s.repository.Get(ctx, key)
	if errors.Is(err, types.ErrProcessingArtifactNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get processing artifact manifest for invalidation: %w", err)
	}
	if artifact == nil {
		return false, errors.New("processing artifact repository returned a nil manifest")
	}
	if artifact.ContentSHA256 != processingArtifactSHA256(observed) {
		return false, nil
	}

	var resolver processingArtifactFileServiceResolver
	var objectPath string
	if artifact.ObjectPath != "" {
		resolver, objectPath, err = s.resolveOwnedObject(ctx, key.TenantID, artifact.ObjectPath)
		if err != nil {
			return false, err
		}
	}

	if artifact.ObjectPath != "" {
		if err := resolver.service.DeleteFile(ctx, filesvc.ServiceStoredPath(resolver.service, objectPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("delete processing artifact object: %w", err)
		}
	}
	removed, err := s.repository.DeleteByIDWithResult(ctx, key.TenantID, artifact.ID)
	if err != nil {
		return false, fmt.Errorf("delete processing artifact manifest: %w", err)
	}
	return removed, nil
}

func (s *processingArtifactStore) PurgeExpired(
	ctx context.Context,
	cutoff time.Time,
	batchSize int,
) (types.ProcessingArtifactPurgeResult, error) {
	if batchSize <= 0 {
		return types.ProcessingArtifactPurgeResult{}, errors.New("processing artifact purge batch size must be positive")
	}

	var result types.ProcessingArtifactPurgeResult
	var representative error
	var afterID uint64
	resolvers := make(map[processingArtifactPurgeResolverKey]processingArtifactPurgeResolverResult)
	for {
		artifacts, err := s.repository.ListExpired(ctx, cutoff, afterID, batchSize)
		if err != nil {
			return result, newProcessingArtifactPurgeFailure(
				"manifest_list",
				fmt.Errorf("list expired processing artifacts: %w", err),
			)
		}
		if len(artifacts) == 0 {
			break
		}

		pageAdvanced := false
		for _, artifact := range artifacts {
			if artifact == nil {
				result.Failed++
				if representative == nil {
					representative = newProcessingArtifactPurgeFailure(
						"manifest_invalid",
						errors.New("processing artifact repository returned a nil expired manifest"),
					)
				}
				continue
			}
			afterID = artifact.ID
			pageAdvanced = true
			result.Scanned++
			removed, err := s.purgeExpiredArtifact(ctx, artifact, resolvers)
			if err != nil {
				result.Failed++
				s.counters.Record(artifact.Stage, "error")
				if representative == nil {
					representative = err
				}
				continue
			}
			if removed {
				result.Deleted++
				result.DeletedBytes += artifact.SizeBytes
				s.counters.Record(artifact.Stage, "evicted")
			}
		}
		if !pageAdvanced {
			return result, fmt.Errorf("purge expired processing artifacts (%d failures): %w", result.Failed, representative)
		}
		if len(artifacts) < batchSize {
			break
		}
	}
	if representative != nil {
		return result, fmt.Errorf("purge expired processing artifacts (%d failures): %w", result.Failed, representative)
	}
	return result, nil
}

func (s *processingArtifactStore) purgeExpiredArtifact(
	ctx context.Context,
	artifact *types.ProcessingArtifact,
	resolvers map[processingArtifactPurgeResolverKey]processingArtifactPurgeResolverResult,
) (bool, error) {
	if artifact.ObjectPath != "" {
		objectPath, ok := processingArtifactObjectPath(artifact.ObjectPath)
		if !ok {
			return false, newProcessingArtifactPurgeFailure("ownership", errProcessingArtifactObjectNotOwned)
		}
		resolver, err := s.resolvePurgeFileService(ctx, artifact.TenantID, objectPath, resolvers)
		if err != nil {
			return false, newProcessingArtifactPurgeFailure("storage_resolve", err)
		}
		if !processingArtifactServiceOwnsPath(resolver, objectPath) {
			return false, newProcessingArtifactPurgeFailure("ownership", errProcessingArtifactObjectNotOwned)
		}
		if err := resolver.service.DeleteFile(ctx, filesvc.ServiceStoredPath(resolver.service, objectPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, newProcessingArtifactPurgeFailure(
				"object_delete",
				fmt.Errorf("delete processing artifact object: %w", err),
			)
		}
	}
	removed, err := s.repository.DeleteByIDWithResult(ctx, artifact.TenantID, artifact.ID)
	if err != nil {
		return false, newProcessingArtifactPurgeFailure(
			"manifest_delete",
			fmt.Errorf("delete processing artifact manifest: %w", err),
		)
	}
	return removed, nil
}

func (s *processingArtifactStore) resolvePurgeFileService(
	ctx context.Context,
	tenantID uint64,
	objectPath string,
	resolvers map[processingArtifactPurgeResolverKey]processingArtifactPurgeResolverResult,
) (processingArtifactFileServiceResolver, error) {
	provider := strings.ToLower(strings.TrimSpace(types.InferStorageFromFilePath(objectPath)))
	backendID, _, _ := types.ParseStorageBackendPath(objectPath)
	key := processingArtifactPurgeResolverKey{tenantID: tenantID, provider: provider, backendID: backendID}
	if cached, ok := resolvers[key]; ok {
		return cached.resolver, cached.err
	}
	resolver, err := s.resolveFileServiceForPath(ctx, tenantID, objectPath)
	resolvers[key] = processingArtifactPurgeResolverResult{resolver: resolver, err: err}
	return resolver, err
}

func (s *processingArtifactStore) validatePayloadSize(value []byte) error {
	if len(value) > s.maxPayloadBytes {
		return errors.New("processing artifact payload exceeds configured maximum")
	}
	return nil
}

func (s *processingArtifactStore) putManySequential(
	ctx context.Context,
	values map[types.ProcessingArtifactKey][]byte,
	progress map[types.ProcessingArtifactKey]processingArtifactWriteProgress,
) (map[types.ProcessingArtifactKey][]byte, map[types.ProcessingArtifactKey]processingArtifactWriteProgress, error) {
	result := make(map[types.ProcessingArtifactKey][]byte, len(values))
	for key, value := range values {
		canonical, _, err := s.putIfAbsent(ctx, key, value)
		if err != nil {
			return nil, progress, err
		}
		result[key] = canonical
		progress[key] = processingArtifactWriteProgress{durable: true, complete: true}
	}
	return result, progress, nil
}

func (s *processingArtifactStore) recordLookup(stage string, hit bool, err error) {
	if err != nil {
		s.counters.Record(stage, "error")
		return
	}
	if hit {
		s.counters.Record(stage, "hit")
		return
	}
	s.counters.Record(stage, "miss")
}

func (s *processingArtifactStore) recordLookupErrors(keys []types.ProcessingArtifactKey) {
	for _, key := range keys {
		s.counters.Record(key.Stage, "error")
	}
}

func (s *processingArtifactStore) recordWrite(stage string, err error) {
	if err != nil {
		s.counters.Record(stage, "error")
		return
	}
	s.counters.Record(stage, "write")
}

func (s *processingArtifactStore) recordBatchWrites(
	values map[types.ProcessingArtifactKey][]byte,
	progress map[types.ProcessingArtifactKey]processingArtifactWriteProgress,
) {
	for key := range values {
		state := progress[key]
		if state.durable {
			s.counters.Record(key.Stage, "write")
		}
		if !state.complete {
			s.counters.Record(key.Stage, "error")
		}
	}
}

func (s *processingArtifactStore) readArtifact(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	artifact *types.ProcessingArtifact,
) ([]byte, bool, error) {
	if artifact == nil {
		return nil, false, errors.New("processing artifact repository returned a nil manifest")
	}

	if artifact.ObjectPath == "" {
		if artifact.Payload == nil || !processingArtifactValueMatches(artifact, artifact.Payload) {
			return s.evictCorrupt(ctx, key, artifact, nil)
		}
		return cloneProcessingArtifactValue(artifact.Payload), true, nil
	}

	if artifact.Payload != nil || artifact.SizeBytes < 0 || artifact.SizeBytes == int64(^uint64(0)>>1) {
		return s.evictCorrupt(ctx, key, artifact, nil)
	}

	objectPath, ok := processingArtifactObjectPath(artifact.ObjectPath)
	if !ok {
		return nil, false, errProcessingArtifactObjectNotOwned
	}
	resolver, err := s.resolveFileServiceForPath(ctx, key.TenantID, objectPath)
	if err != nil {
		return nil, false, err
	}
	authoritative := processingArtifactServiceOwnsPath(resolver, objectPath)
	servicePath := filesvc.ServiceStoredPath(resolver.service, objectPath)
	reader, err := resolver.service.GetFile(ctx, servicePath)
	if errors.Is(err, fs.ErrNotExist) && authoritative {
		return s.evictCorrupt(ctx, key, artifact, &resolver)
	}
	if err != nil {
		return nil, false, fmt.Errorf("read processing artifact object: %w", err)
	}
	if reader == nil {
		return nil, false, errors.New("processing artifact file service returned a nil reader")
	}

	value, readErr := io.ReadAll(io.LimitReader(reader, artifact.SizeBytes+1))
	closeErr := reader.Close()
	if errors.Is(readErr, fs.ErrNotExist) && authoritative {
		return s.evictCorrupt(ctx, key, artifact, &resolver)
	}
	if readErr != nil {
		return nil, false, fmt.Errorf("read processing artifact object: %w", readErr)
	}
	if closeErr != nil {
		return nil, false, fmt.Errorf("close processing artifact object: %w", closeErr)
	}
	if !processingArtifactValueMatches(artifact, value) {
		if !authoritative {
			return nil, false, errors.New("processing artifact object verification failed through non-authoritative file service")
		}
		return s.evictCorrupt(ctx, key, artifact, &resolver)
	}
	return cloneProcessingArtifactValue(value), true, nil
}

func (s *processingArtifactStore) evictCorrupt(
	ctx context.Context,
	key types.ProcessingArtifactKey,
	artifact *types.ProcessingArtifact,
	resolver *processingArtifactFileServiceResolver,
) ([]byte, bool, error) {
	if artifact.ObjectPath != "" {
		objectPath, ok := processingArtifactObjectPath(artifact.ObjectPath)
		if !ok {
			return nil, false, errProcessingArtifactObjectNotOwned
		}
		if resolver == nil {
			resolved, err := s.resolveFileServiceForPath(ctx, key.TenantID, objectPath)
			if err != nil {
				return nil, false, err
			}
			resolver = &resolved
		}
		if !processingArtifactServiceOwnsPath(*resolver, objectPath) {
			return nil, false, errProcessingArtifactObjectNotOwned
		}
		if err := resolver.service.DeleteFile(
			ctx,
			filesvc.ServiceStoredPath(resolver.service, objectPath),
		); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("delete corrupt processing artifact object: %w", err)
		}
	}

	removed, err := s.repository.DeleteByIDWithResult(ctx, key.TenantID, artifact.ID)
	if err != nil {
		return nil, false, fmt.Errorf("evict corrupt processing artifact manifest: %w", err)
	}
	if removed {
		s.counters.Record(key.Stage, "evicted")
	}
	return nil, false, nil
}

func (s *processingArtifactStore) resolveFileService(
	ctx context.Context,
	tenantID uint64,
	backendID string,
	provider string,
) (processingArtifactFileServiceResolver, error) {
	tenant, ok := types.TenantInfoFromContext(ctx)
	if !ok || tenant.ID != tenantID {
		if s.tenantRepository == nil {
			tenant = nil
		} else {
			var err error
			tenant, err = s.tenantRepository.GetTenantByID(ctx, tenantID)
			if err != nil {
				return processingArtifactFileServiceResolver{}, fmt.Errorf("get processing artifact tenant: %w", err)
			}
		}
	}

	if tenant != nil && s.storageResolver != nil {
		localBaseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
		var service interfaces.FileService
		var resolvedProvider string
		var err error
		if historical, ok := s.storageResolver.(processingArtifactHistoricalStorageResolver); ok && backendID != "" {
			service, resolvedProvider, err = historical.ResolveHistoricalFileService(
				ctx, tenant, backendID, localBaseDir,
			)
		} else {
			service, resolvedProvider, err = s.storageResolver.ResolveFileService(
				ctx, tenant, backendID, provider, localBaseDir,
			)
		}
		if err != nil {
			return processingArtifactFileServiceResolver{}, fmt.Errorf("resolve processing artifact file service: %w", err)
		}
		return processingArtifactFileServiceResolver{
			service:   service,
			provider:  strings.ToLower(strings.TrimSpace(resolvedProvider)),
			backendID: filesvc.StorageBackendID(service),
		}, nil
	}

	if tenant == nil || tenant.StorageEngineConfig == nil {
		return s.globalFileServiceResolver()
	}

	if strings.TrimSpace(provider) == "" {
		provider = tenant.StorageEngineConfig.DefaultProvider
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if !processingArtifactProviderConfigured(tenant.StorageEngineConfig, provider) {
		return s.globalFileServiceResolver()
	}
	service, resolvedProvider, err := s.newFileService(
		provider,
		tenant.StorageEngineConfig,
		strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR")),
	)
	if err != nil {
		return processingArtifactFileServiceResolver{}, fmt.Errorf("resolve processing artifact file service: %w", err)
	}
	provider = filesvc.StorageProvider(service)
	if provider == "" {
		provider = resolvedProvider
	}
	return processingArtifactFileServiceResolver{service: service, provider: provider}, nil
}

func (s *processingArtifactStore) resolveFileServiceForPath(
	ctx context.Context,
	tenantID uint64,
	objectPath string,
) (processingArtifactFileServiceResolver, error) {
	backendID, _, _ := types.ParseStorageBackendPath(objectPath)
	return s.resolveFileService(ctx, tenantID, backendID, types.InferStorageFromFilePath(objectPath))
}

func (s *processingArtifactStore) resolveOwnedObject(
	ctx context.Context,
	tenantID uint64,
	reference string,
) (processingArtifactFileServiceResolver, string, error) {
	objectPath, ok := processingArtifactObjectPath(reference)
	if !ok {
		return processingArtifactFileServiceResolver{}, "", errProcessingArtifactObjectNotOwned
	}
	resolver, err := s.resolveFileServiceForPath(ctx, tenantID, objectPath)
	if err != nil {
		return processingArtifactFileServiceResolver{}, "", err
	}
	if !processingArtifactServiceOwnsPath(resolver, objectPath) {
		return processingArtifactFileServiceResolver{}, "", errProcessingArtifactObjectNotOwned
	}
	return resolver, objectPath, nil
}

func (s *processingArtifactStore) globalFileService() (interfaces.FileService, error) {
	if s.fileService == nil {
		return nil, errors.New("processing artifact file service is not configured")
	}
	return s.fileService, nil
}

func (s *processingArtifactStore) globalFileServiceResolver() (processingArtifactFileServiceResolver, error) {
	service, err := s.globalFileService()
	if err != nil {
		return processingArtifactFileServiceResolver{}, err
	}
	return processingArtifactFileServiceResolver{
		service:   service,
		provider:  filesvc.StorageProvider(service),
		backendID: filesvc.StorageBackendID(service),
	}, nil
}

func processingArtifactProviderConfigured(config *types.StorageEngineConfig, provider string) bool {
	if config == nil || provider == "" {
		return false
	}
	switch provider {
	case "local":
		return true
	case "minio":
		return config.MinIO != nil
	case "cos":
		return config.COS != nil
	case "tos":
		return config.TOS != nil
	case "s3":
		return config.S3 != nil
	case "oss":
		return config.OSS != nil
	case "ks3":
		return config.KS3 != nil
	case "obs":
		return config.OBS != nil
	default:
		return false
	}
}

func processingArtifactServiceOwnsPath(
	resolver processingArtifactFileServiceResolver,
	path string,
) bool {
	canonicalPath := filesvc.CanonicalStoredPath(resolver.service, path)
	if backendID, _, ok := types.ParseStorageBackendPath(canonicalPath); ok {
		return backendID != "" && backendID == resolver.backendID
	}
	scheme := processingArtifactPathScheme(canonicalPath)
	if scheme == "http" || scheme == "https" {
		return false
	}
	if scheme == "" {
		return resolver.provider == "local"
	}
	return resolver.provider == scheme
}

func processingArtifactObjectReference(objectPath string) string {
	return processingArtifactObjectReferencePrefix +
		base64.RawURLEncoding.EncodeToString([]byte(objectPath))
}

func processingArtifactObjectPath(reference string) (string, bool) {
	if !strings.HasPrefix(reference, processingArtifactObjectReferencePrefix) {
		return "", false
	}
	encoded := strings.TrimPrefix(reference, processingArtifactObjectReferencePrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || processingArtifactObjectReference(string(decoded)) != reference {
		return "", false
	}
	return string(decoded), true
}

func processingArtifactPathScheme(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 3 && isASCIIAlpha(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return ""
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func (s *processingArtifactStore) deleteCandidate(
	ctx context.Context,
	path string,
	resolver processingArtifactFileServiceResolver,
) {
	if objectPath, ok := processingArtifactObjectPath(path); ok && resolver.service != nil {
		_ = resolver.service.DeleteFile(ctx, filesvc.ServiceStoredPath(resolver.service, objectPath))
	}
}

func processingArtifactValueMatches(artifact *types.ProcessingArtifact, value []byte) bool {
	return artifact.SizeBytes == int64(len(value)) &&
		artifact.ContentSHA256 == processingArtifactSHA256(value)
}

func processingArtifactSHA256(value []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(value))
}

func cloneProcessingArtifactValue(value []byte) []byte {
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}
