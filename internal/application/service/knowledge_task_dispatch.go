package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	documentProcessGlobalGateKey       = "knowledge:process:gate:global"
	documentProcessEngineGateKeyPrefix = "knowledge:process:gate:engine:"
	documentProcessGateTTL             = 90 * time.Second
	documentProcessGateRenewInterval   = 30 * time.Second
	defaultDocumentProcessDelay        = 10 * time.Second
	defaultDocumentProcessRequeueDelay = 5 * time.Second
	defaultDocumentProcessGlobalLimit  = 2
	defaultDocumentProcessEngineLimit  = 2
)

var defaultDocumentProcessEngineLimits = map[string]int{
	"weknoracloud": 1,
	"mineru":       1,
	"mineru_cloud": 1,
}

type documentProcessDispatchConfig struct {
	enqueueDelay       time.Duration
	requeueDelay       time.Duration
	globalLimit        int
	defaultEngineLimit int
	engineLimits       map[string]int
}

type documentProcessGateLease struct {
	once    sync.Once
	release func()
}

func (l *documentProcessGateLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

var documentProcessGateScript = redis.NewScript(`
local key    = KEYS[1]
local maxW   = tonumber(ARGV[1])
local ttlMs  = tonumber(ARGV[2])

local count = redis.call('INCR', key)
redis.call('PEXPIRE', key, ttlMs)
if count <= maxW then
    return 1
end
redis.call('DECR', key)
return 0
`)

func loadDocumentProcessDispatchConfig() documentProcessDispatchConfig {
	engineLimits := make(map[string]int, len(defaultDocumentProcessEngineLimits))
	for engine, limit := range defaultDocumentProcessEngineLimits {
		engineLimits[engine] = limit
	}
	for engine, limit := range parseDocumentProcessEngineLimits(os.Getenv("WEKNORA_DOCUMENT_PROCESS_ENGINE_LIMITS")) {
		engineLimits[engine] = limit
	}

	return documentProcessDispatchConfig{
		enqueueDelay:       parseEnvDurationSeconds("WEKNORA_DOCUMENT_PROCESS_DELAY_SECONDS", defaultDocumentProcessDelay),
		requeueDelay:       parseEnvDurationSeconds("WEKNORA_DOCUMENT_PROCESS_REQUEUE_DELAY_SECONDS", defaultDocumentProcessRequeueDelay),
		globalLimit:        parseEnvNonNegativeInt("WEKNORA_DOCUMENT_PROCESS_GLOBAL_LIMIT", defaultDocumentProcessGlobalLimit),
		defaultEngineLimit: parseEnvNonNegativeInt("WEKNORA_DOCUMENT_PROCESS_ENGINE_LIMIT", defaultDocumentProcessEngineLimit),
		engineLimits:       engineLimits,
	}
}

func parseEnvNonNegativeInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func parseEnvDurationSeconds(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func parseDocumentProcessEngineLimits(raw string) map[string]int {
	limits := map[string]int{}
	for _, item := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(item)
		if entry == "" {
			continue
		}
		parts := strings.FieldsFunc(entry, func(r rune) bool {
			return r == ':' || r == '='
		})
		if len(parts) != 2 {
			continue
		}
		limit, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || limit < 0 {
			continue
		}
		limits[normalizeDocumentProcessEngineName(parts[0])] = limit
	}
	return limits
}

func normalizeDocumentProcessEngineName(engine string) string {
	normalized := strings.ToLower(strings.TrimSpace(engine))
	if normalized == "" {
		return "builtin"
	}
	return normalized
}

func resolveDocumentProcessEngine(payload types.DocumentProcessPayload, kb *types.KnowledgeBase) string {
	if kb == nil {
		return "builtin"
	}

	switch {
	case payload.URL != "":
		return normalizeDocumentProcessEngineName(kb.ChunkingConfig.ResolveParserEngine("url"))
	case len(payload.Passages) > 0:
		return "passage"
	default:
		fileType := strings.ToLower(strings.TrimSpace(payload.FileType))
		if fileType == "" && payload.FileName != "" {
			fileType = getFileType(payload.FileName)
		}
		return normalizeDocumentProcessEngineName(kb.ChunkingConfig.ResolveParserEngine(fileType))
	}
}

func (c documentProcessDispatchConfig) engineLimit(engine string) int {
	if limit, ok := c.engineLimits[normalizeDocumentProcessEngineName(engine)]; ok {
		return limit
	}
	return c.defaultEngineLimit
}

func (s *knowledgeService) enqueueDocumentProcessTask(
	ctx context.Context,
	payload types.DocumentProcessPayload,
) (*asynq.TaskInfo, error) {
	return s.enqueueDocumentProcessTaskWithDelay(ctx, payload, loadDocumentProcessDispatchConfig().enqueueDelay)
}

func (s *knowledgeService) enqueueDocumentProcessTaskWithDelay(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	delay time.Duration,
) (*asynq.TaskInfo, error) {
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal document process task payload: %w", err)
	}

	options := []asynq.Option{asynq.Queue("default")}
	if payload.RetryLimit > 0 {
		options = append(options, asynq.MaxRetry(payload.RetryLimit))
	}
	if delay > 0 {
		options = append(options, asynq.ProcessIn(delay))
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes)
	return s.task.Enqueue(task, options...)
}

func (s *knowledgeService) requeueDocumentProcessTask(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	reason string,
) error {
	config := loadDocumentProcessDispatchConfig()
	delay := config.requeueDelay
	if delay <= 0 {
		delay = defaultDocumentProcessRequeueDelay
	}

	info, err := s.enqueueDocumentProcessTaskWithDelay(ctx, payload, delay)
	if err != nil {
		return err
	}

	logger.Infof(ctx,
		"Requeued document process task: id=%s reason=%s knowledge_id=%s delay=%s",
		info.ID,
		reason,
		payload.KnowledgeID,
		delay,
	)
	return nil
}

func (s *knowledgeService) tryAcquireDocumentProcessLease(
	ctx context.Context,
	engine string,
) (*documentProcessGateLease, bool) {
	config := loadDocumentProcessDispatchConfig()
	engine = normalizeDocumentProcessEngineName(engine)
	engineLimit := config.engineLimit(engine)
	if config.globalLimit <= 0 && engineLimit <= 0 {
		return &documentProcessGateLease{}, true
	}

	if s.redisClient != nil {
		lease, acquired, err := s.tryAcquireRedisDocumentProcessLease(ctx, engine, config.globalLimit, engineLimit)
		if err == nil {
			return lease, acquired
		}
		logger.Warnf(ctx, "Document process gate Redis unavailable, falling back to local gate: %v", err)
	}

	return s.tryAcquireLocalDocumentProcessLease(engine, config.globalLimit, engineLimit)
}

func (s *knowledgeService) tryAcquireRedisDocumentProcessLease(
	ctx context.Context,
	engine string,
	globalLimit int,
	engineLimit int,
) (*documentProcessGateLease, bool, error) {
	if s.redisClient == nil {
		return nil, false, fmt.Errorf("redis client unavailable")
	}

	acquiredKeys := make([]string, 0, 2)
	tryAcquire := func(key string, limit int) (bool, error) {
		if limit <= 0 {
			return true, nil
		}
		result, err := documentProcessGateScript.Run(ctx, s.redisClient,
			[]string{key},
			limit, documentProcessGateTTL.Milliseconds(),
		).Int64()
		if err != nil {
			return false, err
		}
		if result == 1 {
			acquiredKeys = append(acquiredKeys, key)
			return true, nil
		}
		return false, nil
	}

	releaseKeys := func() {
		for _, key := range acquiredKeys {
			s.redisClient.Decr(context.Background(), key)
		}
	}

	if ok, err := tryAcquire(documentProcessGlobalGateKey, globalLimit); err != nil {
		return nil, false, err
	} else if !ok {
		return nil, false, nil
	}

	engineKey := documentProcessEngineGateKeyPrefix + engine
	if ok, err := tryAcquire(engineKey, engineLimit); err != nil {
		releaseKeys()
		return nil, false, err
	} else if !ok {
		releaseKeys()
		return nil, false, nil
	}

	lease := &documentProcessGateLease{}
	if len(acquiredKeys) == 0 {
		return lease, true, nil
	}

	renewCtx, cancel := context.WithCancel(context.Background())
	go func(keys []string) {
		ticker := time.NewTicker(documentProcessGateRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				for _, key := range keys {
					s.redisClient.Expire(context.Background(), key, documentProcessGateTTL)
				}
			}
		}
	}(append([]string(nil), acquiredKeys...))

	lease.release = func() {
		cancel()
		releaseKeys()
	}
	return lease, true, nil
}

func (s *knowledgeService) tryAcquireLocalDocumentProcessLease(
	engine string,
	globalLimit int,
	engineLimit int,
) (*documentProcessGateLease, bool) {
	s.documentGateMu.Lock()
	defer s.documentGateMu.Unlock()

	if s.documentGateByEngine == nil {
		s.documentGateByEngine = make(map[string]int)
	}

	if globalLimit > 0 && s.documentGateAll >= globalLimit {
		return nil, false
	}
	if engineLimit > 0 && s.documentGateByEngine[engine] >= engineLimit {
		return nil, false
	}

	if globalLimit > 0 {
		s.documentGateAll++
	}
	if engineLimit > 0 {
		s.documentGateByEngine[engine]++
	}

	return &documentProcessGateLease{release: func() {
		s.documentGateMu.Lock()
		defer s.documentGateMu.Unlock()
		if globalLimit > 0 && s.documentGateAll > 0 {
			s.documentGateAll--
		}
		if engineLimit > 0 {
			current := s.documentGateByEngine[engine]
			if current <= 1 {
				delete(s.documentGateByEngine, engine)
			} else {
				s.documentGateByEngine[engine] = current - 1
			}
		}
	}}, true
}
