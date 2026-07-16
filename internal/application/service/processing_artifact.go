package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"strings"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	ProcessingArtifactInlineLimit = 1 << 20
	processingArtifactPutAttempts = 3
)

type processingArtifactStore struct {
	repository       interfaces.ProcessingArtifactRepository
	tenantRepository interfaces.TenantRepository
	fileService      interfaces.FileService
	newFileService   processingArtifactFileServiceFactory
}

type processingArtifactBatchRepository interface {
	GetMany(ctx context.Context, keys []types.ProcessingArtifactKey) (
		map[types.ProcessingArtifactKey]*types.ProcessingArtifact, error,
	)
	PutManyIfAbsent(ctx context.Context, artifacts []*types.ProcessingArtifact) error
}

type processingArtifactFileServiceFactory func(
	provider string,
	storageConfig *types.StorageEngineConfig,
	localBaseDir string,
) (interfaces.FileService, string, error)

type processingArtifactFileServiceResolver struct {
	service  interfaces.FileService
	provider string
}

func NewProcessingArtifactStore(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
) interfaces.ProcessingArtifactStore {
	return newProcessingArtifactStore(
		repository,
		tenantRepository,
		fileService,
		filesvc.NewFileServiceFromStorageConfig,
	)
}

func newProcessingArtifactStore(
	repository interfaces.ProcessingArtifactRepository,
	tenantRepository interfaces.TenantRepository,
	fileService interfaces.FileService,
	newFileService processingArtifactFileServiceFactory,
) *processingArtifactStore {
	return &processingArtifactStore{
		repository:       repository,
		tenantRepository: tenantRepository,
		fileService:      fileService,
		newFileService:   newFileService,
	}
}

func (s *processingArtifactStore) Get(
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
		for _, key := range keys {
			value, hit, err := s.Get(ctx, key)
			if err != nil {
				return nil, err
			}
			if hit {
				result[key] = value
			}
		}
		return result, nil
	}

	artifacts, err := batchRepository.GetMany(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get processing artifact manifests: %w", err)
	}
	for _, key := range keys {
		artifact, ok := artifacts[key]
		if !ok {
			continue
		}
		value, hit, err := s.readArtifact(ctx, key, artifact)
		if err != nil {
			return nil, err
		}
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
			candidateResolver, err = s.resolveFileService(ctx, key.TenantID, "")
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
			artifact.ObjectPath = filesvc.CanonicalStoredPath(candidateResolver.service, artifact.ObjectPath)
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
	result := make(map[types.ProcessingArtifactKey][]byte, len(values))
	batchRepository, ok := s.repository.(processingArtifactBatchRepository)
	if !ok {
		return s.putManySequential(ctx, values)
	}

	inlineValues := make(map[types.ProcessingArtifactKey][]byte, len(values))
	artifacts := make([]*types.ProcessingArtifact, 0, len(values))
	for key, value := range values {
		if len(value) > ProcessingArtifactInlineLimit {
			canonical, _, err := s.PutIfAbsent(ctx, key, value)
			if err != nil {
				return nil, err
			}
			result[key] = canonical
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
		return result, nil
	}
	if err := batchRepository.PutManyIfAbsent(ctx, artifacts); err != nil {
		return nil, fmt.Errorf("put processing artifact manifests: %w", err)
	}

	keys := make([]types.ProcessingArtifactKey, 0, len(inlineValues))
	for key := range inlineValues {
		keys = append(keys, key)
	}
	winners, err := batchRepository.GetMany(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get winning processing artifact manifests: %w", err)
	}
	for key, candidate := range inlineValues {
		winner, ok := winners[key]
		if ok {
			canonical, hit, readErr := s.readArtifact(ctx, key, winner)
			if readErr != nil {
				return nil, readErr
			}
			if hit {
				result[key] = canonical
				continue
			}
		}
		canonical, _, putErr := s.PutIfAbsent(ctx, key, candidate)
		if putErr != nil {
			return nil, putErr
		}
		result[key] = canonical
	}
	return result, nil
}

func (s *processingArtifactStore) putManySequential(
	ctx context.Context,
	values map[types.ProcessingArtifactKey][]byte,
) (map[types.ProcessingArtifactKey][]byte, error) {
	result := make(map[types.ProcessingArtifactKey][]byte, len(values))
	for key, value := range values {
		canonical, _, err := s.PutIfAbsent(ctx, key, value)
		if err != nil {
			return nil, err
		}
		result[key] = canonical
	}
	return result, nil
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

	resolver, err := s.resolveFileService(ctx, key.TenantID, types.InferStorageFromFilePath(artifact.ObjectPath))
	if err != nil {
		return nil, false, err
	}
	authoritative := processingArtifactServiceOwnsPath(resolver, artifact.ObjectPath)
	servicePath := filesvc.ServiceStoredPath(resolver.service, artifact.ObjectPath)
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
	if err := s.repository.DeleteByID(ctx, key.TenantID, artifact.ID); err != nil {
		return nil, false, fmt.Errorf("evict corrupt processing artifact manifest: %w", err)
	}
	if artifact.ObjectPath != "" {
		if resolver == nil {
			resolved, err := s.resolveFileService(
				ctx,
				key.TenantID,
				types.InferStorageFromFilePath(artifact.ObjectPath),
			)
			if err == nil {
				resolver = &resolved
			}
		}
		if resolver != nil && processingArtifactServiceOwnsPath(*resolver, artifact.ObjectPath) {
			_ = resolver.service.DeleteFile(ctx, filesvc.ServiceStoredPath(resolver.service, artifact.ObjectPath))
		}
	}
	return nil, false, nil
}

func (s *processingArtifactStore) resolveFileService(
	ctx context.Context,
	tenantID uint64,
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
		service:  service,
		provider: filesvc.StorageProvider(service),
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
	scheme := processingArtifactPathScheme(canonicalPath)
	if scheme == "http" || scheme == "https" {
		return false
	}
	if scheme == "" {
		return resolver.provider == "local"
	}
	return resolver.provider == scheme
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
	if path != "" && resolver.service != nil {
		_ = resolver.service.DeleteFile(ctx, filesvc.ServiceStoredPath(resolver.service, path))
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
