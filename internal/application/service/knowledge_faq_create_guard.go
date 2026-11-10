package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
)

// faqCreateGuardTTL bounds how long one CreateFAQEntry may hold the
// per-question guard. A single create runs the embedding call inline and that
// call retries with exponential backoff, so the worst case is well past ten
// seconds; the TTL only has to outlive that while still releasing the question
// reasonably soon after an instance dies mid-create.
const faqCreateGuardTTL = 60 * time.Second

// faqCreateGuardReleaseTimeout caps the Redis DEL issued on release so a slow
// Redis cannot extend the request past the work it already finished.
const faqCreateGuardReleaseTimeout = 2 * time.Second

// faqCreateInflight is the single-instance fallback used when Redis is absent
// (Lite mode) or momentarily unreachable.
var faqCreateInflight sync.Map // guard key -> struct{}

// faqCreateGuardKey derives the guard key from the standard question. The
// question is hashed rather than embedded verbatim so arbitrary user text
// cannot inject separators into the Redis key namespace.
func faqCreateGuardKey(tenantID uint64, kbID string, standardQuestion string) string {
	sum := sha256.Sum256([]byte(standardQuestion))
	return fmt.Sprintf("faq:create:%d:%s:%s", tenantID, kbID, hex.EncodeToString(sum[:16]))
}

// acquireFAQCreateGuard serializes concurrent creates of the same standard
// question within a knowledge base and returns a release function.
//
// The duplicate-question check can only see entries that are already committed,
// but a create stays in flight for seconds while its embedding is computed. An
// upstream that retries before the first call answers therefore lands several
// identical creates on different instances, each passing the check and each
// inserting its own row. This guard closes that window: the first caller wins
// and the retries get a conflict instead of a second row.
//
// When Redis is unavailable the guard degrades to a process-local map rather
// than rejecting the write, so a Redis outage cannot block FAQ authoring; the
// cross-instance protection is simply lost for the duration.
func (s *knowledgeService) acquireFAQCreateGuard(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	standardQuestion string,
) (func(), error) {
	key := faqCreateGuardKey(tenantID, kbID, standardQuestion)

	if s.redisClient != nil {
		acquired, err := s.redisClient.SetNX(ctx, key, "1", faqCreateGuardTTL).Result()
		switch {
		case err != nil:
			logger.Warnf(ctx,
				"CreateFAQEntry: Redis guard unavailable, falling back to in-process guard: %v", err)
		case !acquired:
			return nil, faqCreateConflictError()
		default:
			return func() {
				releaseCtx, cancel := context.WithTimeout(
					context.WithoutCancel(ctx), faqCreateGuardReleaseTimeout)
				defer cancel()
				if err := s.redisClient.Del(releaseCtx, key).Err(); err != nil {
					logger.Warnf(releaseCtx,
						"CreateFAQEntry: failed to release guard %s (expires in %s): %v",
						key, faqCreateGuardTTL, err)
				}
			}, nil
		}
	}

	if _, loaded := faqCreateInflight.LoadOrStore(key, struct{}{}); loaded {
		return nil, faqCreateConflictError()
	}
	return func() { faqCreateInflight.Delete(key) }, nil
}

func faqCreateConflictError() error {
	return werrors.NewConflictError("相同标准问的 FAQ 条目正在创建中，请勿重复提交")
}
