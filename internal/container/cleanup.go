package container

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// cleanupStepTimeout bounds one shutdown hook that waits on in-flight work
// (cron jobs, retention sweeps). Several of those hooks run before child
// processes are reaped; an unbounded wait used to hold SIGTERM until the
// supervisor killed the process and skipped the rest.
const cleanupStepTimeout = 5 * time.Second

type cleanupEntry struct {
	name string
	fn   types.CleanupFunc
}

// ResourceCleaner is a resource cleaner that can be used to clean up resources
type ResourceCleaner struct {
	mu       sync.Mutex
	cleanups []cleanupEntry
}

// NewResourceCleaner creates a new resource cleaner
func NewResourceCleaner() interfaces.ResourceCleaner {
	return &ResourceCleaner{
		cleanups: make([]cleanupEntry, 0),
	}
}

// Register registers a cleanup function.
// The cleanup function runs in reverse order (the last registered runs first).
func (c *ResourceCleaner) Register(cleanup types.CleanupFunc) {
	c.register("", cleanup)
}

// RegisterWithName registers a cleanup function with a name, for logging tracking.
func (c *ResourceCleaner) RegisterWithName(name string, cleanup types.CleanupFunc) {
	if cleanup == nil {
		return
	}

	wrappedCleanup := func() error {
		log.Printf("Cleaning up resource: %s", name)
		err := cleanup()
		if err != nil {
			log.Printf("Error cleaning up resource %s: %v", name, err)
		} else {
			log.Printf("Successfully cleaned up resource: %s", name)
		}
		return err
	}

	c.register(name, wrappedCleanup)
}

func (c *ResourceCleaner) register(name string, cleanup types.CleanupFunc) {
	if cleanup == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanups = append(c.cleanups, cleanupEntry{name: name, fn: cleanup})
}

// Promote moves every callback registered under name to the end of the list.
// Cleanup runs in reverse, so the promoted callbacks run first. BrowserSkill
// is registered during early container wiring and would otherwise run last,
// after hooks that wait on in-flight jobs.
func (c *ResourceCleaner) Promote(name string) {
	if name == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	matched := make([]cleanupEntry, 0, 1)
	rest := make([]cleanupEntry, 0, len(c.cleanups))
	for _, entry := range c.cleanups {
		if entry.name == name {
			matched = append(matched, entry)
			continue
		}
		rest = append(rest, entry)
	}
	c.cleanups = append(rest, matched...)
}

// Cleanup executes all cleanup functions.
// A cancelled or expired context does not skip remaining functions: the
// deadline used to return on the first select and drop every later hook,
// including ones registered earliest (BrowserSkill). Context errors are not
// reported as hook failures when the hooks themselves still ran.
func (c *ResourceCleaner) Cleanup(ctx context.Context) (errs []error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	for i := len(c.cleanups) - 1; i >= 0; i-- {
		if err := c.cleanups[i].fn(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := ctx.Err(); err != nil {
		log.Printf("Resource cleanup continued past its deadline: %v", err)
	}
	return errs
}

// Reset clears all registered cleanup functions
func (c *ResourceCleaner) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanups = make([]cleanupEntry, 0)
}
