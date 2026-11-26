package tools

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// mcpRevalidateCooldown bounds how often one directory is re-listed. A model
	// that keeps calling a tool name the server does not have, or a server whose
	// instructions differ on every connection, must not cost a tools/list per
	// call.
	mcpRevalidateCooldown = 5 * time.Minute
	// mcpRevalidateTimeout bounds a re-list: a background one that no request
	// waits on, and one inside a failed call, which must not hold the call open
	// for the whole call window.
	mcpRevalidateTimeout = 30 * time.Second
	// mcpRevalidatePruneAt is the number of remembered attempts above which
	// attempts older than the cooldown are dropped.
	mcpRevalidatePruneAt = 1024
)

// MCPDirectoryRevalidator re-lists saved MCP tool directories that may be out
// of date. Saved directories are only replaced by an explicit refresh, so a
// server redeployed with new tools or instructions would otherwise keep being
// described from the old snapshot. That matters most for OAuth services, whose
// directories are saved per user and cannot be refreshed by an administrator
// on the users' behalf.
//
// One instance serves the whole process: agent engines are created per turn,
// and the cooldown has to outlive them.
type MCPDirectoryRevalidator struct {
	maxAge   time.Duration
	cooldown time.Duration
	timeout  time.Duration
	now      func() time.Time

	mu       sync.Mutex
	attempts map[string]time.Time
	inflight map[string]*mcpRevalidation
	// joined, when set, is told that a caller is waiting on a running attempt.
	joined func(key string)
}

// mcpRevalidation is one running re-list; tools and err are set before done
// closes.
type mcpRevalidation struct {
	done  chan struct{}
	tools []*types.MCPTool
	err   error
}

// NewMCPDirectoryRevalidator returns a revalidator that also re-lists a saved
// directory once it is older than maxAge. A maxAge of zero or less disables
// that age check; mismatches seen on live connections still trigger a re-list.
func NewMCPDirectoryRevalidator(maxAge time.Duration) *MCPDirectoryRevalidator {
	return &MCPDirectoryRevalidator{
		maxAge:   maxAge,
		cooldown: mcpRevalidateCooldown,
		timeout:  mcpRevalidateTimeout,
		now:      time.Now,
		attempts: make(map[string]time.Time),
		inflight: make(map[string]*mcpRevalidation),
	}
}

// expired reports whether a directory saved at syncedAt is older than the
// configured maximum age. An unknown save time is never treated as expired.
func (r *MCPDirectoryRevalidator) expired(syncedAt time.Time) bool {
	if r == nil || r.maxAge <= 0 || syncedAt.IsZero() {
		return false
	}
	return r.now().Sub(syncedAt) > r.maxAge
}

// trigger runs relist for key unless the key was attempted within the
// cooldown. An attempt counts from its start, whether or not it succeeds, so a
// failing server is not asked again on every call.
//
// With wait set, relist runs on the caller's goroutine, for at most the
// re-list timeout, and trigger returns once it is done; a caller that
// finds an attempt already running waits for it. The result is the re-listed
// directory and whether a re-list succeeded while the caller waited.
// Without wait, relist runs in the background, keeping the request's values
// (tenant, principal) but not its cancellation, and trigger returns at once.
func (r *MCPDirectoryRevalidator) trigger(
	ctx context.Context,
	key, reason string,
	wait bool,
	relist func(context.Context) ([]*types.MCPTool, error),
) ([]*types.MCPTool, bool) {
	if r == nil || relist == nil {
		return nil, false
	}
	r.mu.Lock()
	if running, ok := r.inflight[key]; ok {
		joined := r.joined
		r.mu.Unlock()
		if !wait {
			return nil, false
		}
		if joined != nil {
			joined(key)
		}
		select {
		case <-running.done:
			return running.tools, running.err == nil
		case <-ctx.Done():
			return nil, false
		}
	}
	now := r.now()
	if last, ok := r.attempts[key]; ok && now.Sub(last) < r.cooldown {
		r.mu.Unlock()
		return nil, false
	}
	r.rememberLocked(key, now)
	attempt := &mcpRevalidation{done: make(chan struct{})}
	r.inflight[key] = attempt
	r.mu.Unlock()

	run := func(runCtx context.Context) {
		defer func() {
			r.mu.Lock()
			delete(r.inflight, key)
			r.mu.Unlock()
			close(attempt.done)
		}()
		attempt.tools, attempt.err = relist(runCtx)
		if attempt.err != nil {
			logger.GetLogger(runCtx).Warnf("MCP directory revalidation failed reason=%q: %v", reason, attempt.err)
			return
		}
		logger.GetLogger(runCtx).Infof("MCP directory revalidated reason=%q", reason)
	}
	if wait {
		runCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		run(runCtx)
		return attempt.tools, attempt.err == nil
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
		defer cancel()
		defer RecoverGoroutine(runCtx, "MCP directory revalidation", nil)
		run(runCtx)
	}()
	return nil, false
}

func (r *MCPDirectoryRevalidator) rememberLocked(key string, at time.Time) {
	if len(r.attempts) >= mcpRevalidatePruneAt {
		for k, last := range r.attempts {
			if at.Sub(last) >= r.cooldown {
				delete(r.attempts, k)
			}
		}
	}
	r.attempts[key] = at
}

// mcpRevalidationKey identifies one saved directory: OAuth services keep one
// per authorizing principal, every other service one per tenant.
func mcpRevalidationKey(ctx context.Context, service *types.MCPService) string {
	tenant, _ := types.TenantIDFromContext(ctx)
	principal := ""
	if service.AuthConfig.IsOAuth() {
		principal = types.MCPOAuthPrincipalFromContext(ctx).StorageID()
	}
	return strconv.FormatUint(tenant, 10) + "\x00" + service.ID + "\x00" + principal
}
