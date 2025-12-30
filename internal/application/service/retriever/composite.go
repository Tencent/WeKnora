package retriever

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"go.opentelemetry.io/otel/attribute"
)

// engineInfo holds information about a retrieve engine and its supported retriever types
type engineInfo struct {
	retrieveEngine interfaces.RetrieveEngineService
	retrieverType  []types.RetrieverType
}

// CompositeRetrieveEngine implements a composite pattern for retrieval engines,
// delegating operations to all registered engines
type CompositeRetrieveEngine struct {
	engineInfos []*engineInfo
}

// Retrieve performs retrieval operations by delegating to the appropriate engine
// based on the retriever type specified in the parameters
func (c *CompositeRetrieveEngine) Retrieve(ctx context.Context,
	retrieveParams []types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	return concurrentRetrieve(ctx, retrieveParams,
		func(ctx context.Context, param types.RetrieveParams, results *[]*types.RetrieveResult, mu *sync.Mutex) error {
			found := false
			for _, engineInfo := range c.engineInfos {
				if engineInfo == nil {
					continue
				}
				if slices.Contains(engineInfo.retrieverType, param.RetrieverType) {
					result, err := engineInfo.retrieveEngine.Retrieve(ctx, param)
					if err != nil {
						return err
					}
					mu.Lock()
					*results = append(*results, result...)
					mu.Unlock()
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("retriever type %s not found", param.RetrieverType)
			}
			return nil
		},
	)
}

// NewCompositeRetrieveEngine creates a new composite retrieve engine with the given parameters
func NewCompositeRetrieveEngine(
	registry interfaces.RetrieveEngineRegistry,
	engineParams []types.RetrieverEngineParams,
) (*CompositeRetrieveEngine, error) {
	engineInfos := make(map[types.RetrieverEngineType]*engineInfo)
	for _, engineParam := range engineParams {
		repo, err := registry.GetRetrieveEngineService(engineParam.RetrieverEngineType)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(repo.Support(), engineParam.RetrieverType) {
			return nil, fmt.Errorf("retrieval engine %s does not support retriever type: %s",
				repo.EngineType(), engineParam.RetrieverType)
		}
		if _, exists := engineInfos[repo.EngineType()]; exists {
			engineInfos[repo.EngineType()].retrieverType = append(engineInfos[repo.EngineType()].retrieverType,
				engineParam.RetrieverType)
			continue
		}
		engineInfos[repo.EngineType()] = &engineInfo{
			retrieveEngine: repo,
			retrieverType:  []types.RetrieverType{engineParam.RetrieverType},
		}
	}
	return &CompositeRetrieveEngine{engineInfos: slices.Collect(maps.Values(engineInfos))}, nil
}

// SupportRetriever checks if a retriever type is supported by any of the registered engines
func (c *CompositeRetrieveEngine) SupportRetriever(r types.RetrieverType) bool {
	for _, engineInfo := range c.engineInfos {
		if engineInfo == nil {
			continue
		}
		if slices.Contains(engineInfo.retrieverType, r) {
			return true
		}
	}
	return false
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (c *CompositeRetrieveEngine) BatchUpdateChunkEnabledStatus(
	ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.BatchUpdateChunkEnabledStatus(ctx, chunkStatusMap); err != nil {
			return err
		}
		return nil
	})
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (c *CompositeRetrieveEngine) BatchUpdateChunkTagID(
	ctx context.Context,
	chunkTagMap map[string]string,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.BatchUpdateChunkTagID(ctx, chunkTagMap); err != nil {
			return err
		}
		return nil
	})
}

// concurrentRetrieve is a helper function for concurrent processing of retrieval parameters
// and collecting results
func concurrentRetrieve(
	ctx context.Context,
	retrieveParams []types.RetrieveParams,
	fn func(ctx context.Context, param types.RetrieveParams, results *[]*types.RetrieveResult, mu *sync.Mutex) error,
) ([]*types.RetrieveResult, error) {
	var results []*types.RetrieveResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(retrieveParams))

	for _, param := range retrieveParams {
		wg.Add(1)
		p := param // Create local copy for safe use in closure
		go func() {
			defer wg.Done()
			if err := fn(ctx, p, &results, &mu); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// concurrentExecWithError is a generic function for concurrent execution of operations
// and handling errors
func (c *CompositeRetrieveEngine) concurrentExecWithError(
	ctx context.Context,
	fn func(ctx context.Context, engineInfo *engineInfo) error,
) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(c.engineInfos))

	for _, engineInfo := range c.engineInfos {
		wg.Add(1)
		eng := engineInfo // Create local copy for safe use in closure
		go func() {
			defer wg.Done()
			if err := fn(ctx, eng); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// Return the first error (if any)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// Index saves vector embeddings to all registered repositories
func (c *CompositeRetrieveEngine) Index(ctx context.Context,
	embedder embedding.Embedder, indexInfo *types.IndexInfo,
) error {
	ctx, span := tracing.ContextWithSpan(ctx, "CompositeRetrieveEngine.Index")
	defer span.End()
	err := c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.Index(ctx, embedder, indexInfo, engineInfo.retrieverType); err != nil {
			logger.Errorf(ctx, "Repository %s failed to save: %v", engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
	span.RecordError(err)
	span.SetAttributes(
		attribute.String("embedder", embedder.GetModelName()),
		attribute.String("source_id", indexInfo.SourceID),
	)
	return err
}

// BatchIndex batch saves vector embeddings to all registered repositories
func (c *CompositeRetrieveEngine) BatchIndex(ctx context.Context,
	embedder embedding.Embedder, indexInfoList []*types.IndexInfo,
) error {
	ctx, span := tracing.ContextWithSpan(ctx, "CompositeRetrieveEngine.BatchIndex")
	defer span.End()
	// Deduplicate sourceIDs
	indexInfoList = common.Deduplicate(func(info *types.IndexInfo) string { return info.SourceID }, indexInfoList...)
	err := c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.BatchIndex(
			ctx,
			embedder,
			indexInfoList,
			engineInfo.retrieverType,
		); err != nil {
			logger.Errorf(ctx, "Repository %s failed to batch save: %v", engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
	span.RecordError(err)
	span.SetAttributes(
		attribute.String("embedder", embedder.GetModelName()),
		attribute.Int("index_info_count", len(indexInfoList)),
	)
	return err
}

// DeleteByChunkIDList deletes vector embeddings by chunk ID list from all registered repositories
func (c *CompositeRetrieveEngine) DeleteByChunkIDList(ctx context.Context,
	chunkIDList []string, dimension int, knowledgeType string,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.DeleteByChunkIDList(ctx, chunkIDList, dimension, knowledgeType); err != nil {
			logger.GetLogger(ctx).Errorf("Repository %s failed to delete chunk ID list: %v",
				engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
}

// DeleteBySourceIDList deletes vector embeddings by source ID list from all registered repositories
func (c *CompositeRetrieveEngine) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.DeleteBySourceIDList(ctx, sourceIDList, dimension, knowledgeType); err != nil {
			logger.GetLogger(ctx).Errorf("Repository %s failed to delete source ID list: %v",
				engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
}

// CopyIndices copies indices from a source knowledge base to a target knowledge base
func (c *CompositeRetrieveEngine) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	targetKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	dimension int,
	knowledgeType string,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.CopyIndices(
			ctx,
			sourceKnowledgeBaseID,
			sourceToTargetKBIDMap,
			sourceToTargetChunkIDMap,
			targetKnowledgeBaseID,
			dimension,
			knowledgeType,
		); err != nil {
			logger.Errorf(ctx, "Repository %s failed to copy indices: %v", engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
}

// DeleteByKnowledgeIDList deletes vector embeddings by knowledge ID list from all registered repositories
func (c *CompositeRetrieveEngine) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	return c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		if err := engineInfo.retrieveEngine.DeleteByKnowledgeIDList(ctx, knowledgeIDList, dimension, knowledgeType); err != nil {
			logger.GetLogger(ctx).Errorf("Repository %s failed to delete knowledge ID list: %v",
				engineInfo.retrieveEngine.EngineType(), err)
			return err
		}
		return nil
	})
}

// EstimateStorageSize estimates the storage size required for the provided index information
func (c *CompositeRetrieveEngine) EstimateStorageSize(ctx context.Context,
	embedder embedding.Embedder, indexInfoList []*types.IndexInfo,
) int64 {
	ctx, span := tracing.ContextWithSpan(ctx, "CompositeRetrieveEngine.EstimateStorageSize")
	defer span.End()
	sum := atomic.Int64{}
	err := c.concurrentExecWithError(ctx, func(ctx context.Context, engineInfo *engineInfo) error {
		sum.Add(engineInfo.retrieveEngine.EstimateStorageSize(ctx, embedder, indexInfoList, engineInfo.retrieverType))
		return nil
	})
	span.RecordError(err)
	span.SetAttributes(
		attribute.String("embedder", embedder.GetModelName()),
		attribute.Int("index_info_count", len(indexInfoList)),
		attribute.Int64("storage_size", sum.Load()),
	)
	return sum.Load()
}
