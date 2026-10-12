package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gopkg.in/yaml.v3"
)

// Inbox event policy — which event types enter the agent inbox, at what
// priority, and how long they live. Defaults live in code (defaultInboxPolicy)
// so the inbox works out of the box; config/agent_inbox_policy.yaml overrides
// entries by type and can add new types. Unknown event types never enter the
// inbox unless the policy explicitly names them ("default deny").
//
// Files: works/agent-sidebar-inbox/plan.md §4.2.

// InboxPolicyEntry is the per-event-type policy.
type InboxPolicyEntry struct {
	Priority  string        `yaml:"priority" json:"priority"`
	Retention time.Duration `yaml:"-" json:"retention_hours"`
	// Debounce drops a repeat event of the same type+dedup key within the
	// window instead of inserting a new row (used for view events).
	Debounce time.Duration `yaml:"-" json:"debounce_seconds"`
	// Enabled=false routes the type to logs only.
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// inboxPolicyFile mirrors config/agent_inbox_policy.yaml.
type inboxPolicyFile struct {
	EventPolicy map[string]struct {
		Priority       string `yaml:"priority"`
		RetentionHours int    `yaml:"retention_hours"`
		DebounceSecs   int    `yaml:"debounce_seconds"`
		Enabled        *bool  `yaml:"enabled"`
	} `yaml:"event_policy"`
}

type inboxPolicy struct {
	mu      sync.RWMutex
	entries map[string]InboxPolicyEntry
	// viewDedup remembers the last accepted (type, dedupKey) per tenant+kb
	// so repeated view events within the debounce window collapse.
	lastAccepted map[string]time.Time
}

// defaultInboxPolicy covers the MVP event set. Anything not listed here and
// not present in the yaml override is log-only ("default deny").
func defaultInboxPolicy() *inboxPolicy {
	p := &inboxPolicy{
		entries: map[string]InboxPolicyEntry{
			types.InboxTypeUserViewedPage: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Debounce:  2 * time.Minute,
				Enabled:   true,
			},
			types.InboxTypeUserViewedDocument: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Debounce:  time.Minute,
				Enabled:   true,
			},
			types.InboxTypeUserViewedImage: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Debounce:  time.Minute,
				Enabled:   true,
			},
			types.InboxTypeUserSwitchedTab: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Debounce:  time.Minute,
				Enabled:   true,
			},
			types.InboxTypeUserSwitchedKB: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Debounce:  time.Minute,
				Enabled:   true,
			},
			types.InboxTypeUserErrorToast: {
				Priority:  types.InboxPriorityP0,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserUploadedDoc: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserDeletedDoc: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserReparseDoc: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserEditedWiki: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserEditChunk: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserUpdatedDoc: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserCancelledParse: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserUpdatedKB: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserDeletedKB: {
				Priority:  types.InboxPriorityP0,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeUserEditedFAQ: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeParseCompleted: {
				Priority:  types.InboxPriorityP2,
				Retention: 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeParseFailed: {
				Priority:  types.InboxPriorityP0,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeAgentWikiWrite: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeAgentDocReparse: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
			types.InboxTypeAgentDocCancelParse: {
				Priority:  types.InboxPriorityP1,
				Retention: 7 * 24 * time.Hour,
				Enabled:   true,
			},
		},
		lastAccepted: map[string]time.Time{},
	}
	return p
}

// loadInboxPolicy builds the effective policy: code defaults, then yaml
// overrides when config/agent_inbox_policy.yaml exists.
func loadInboxPolicy(configDir string) *inboxPolicy {
	p := defaultInboxPolicy()
	if configDir == "" {
		return p
	}
	data, err := os.ReadFile(filepath.Join(configDir, "agent_inbox_policy.yaml"))
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warnf(context.Background(), "read agent_inbox_policy.yaml: %v (using defaults)", err)
		}
		return p
	}
	var file inboxPolicyFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		logger.Warnf(context.Background(), "parse agent_inbox_policy.yaml: %v (using defaults)", err)
		return p
	}
	for typ, e := range file.EventPolicy {
		entry := InboxPolicyEntry{
			Priority:  e.Priority,
			Retention: time.Duration(e.RetentionHours) * time.Hour,
			Debounce:  time.Duration(e.DebounceSecs) * time.Second,
			Enabled:   e.Enabled == nil || *e.Enabled,
		}
		if entry.Priority == "" {
			entry.Priority = types.InboxPriorityP2
		}
		if entry.Retention <= 0 {
			entry.Retention = 24 * time.Hour
		}
		p.entries[typ] = entry
	}
	return p
}

// lookup returns the policy entry for a type; ok=false means log-only.
func (p *inboxPolicy) lookup(eventType string) (InboxPolicyEntry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.entries[eventType]
	return e, ok && e.Enabled
}

// acceptDebounced reports whether an event with the given dedup key should
// be inserted now (true) or collapsed into a recent one (false).
func (p *inboxPolicy) acceptDebounced(eventType, dedupKey string, now time.Time) bool {
	if dedupKey == "" {
		return true
	}
	key := eventType + "|" + dedupKey
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[eventType]
	if !ok || entry.Debounce <= 0 {
		return true
	}
	if last, seen := p.lastAccepted[key]; seen && now.Sub(last) < entry.Debounce {
		return false
	}
	p.lastAccepted[key] = now
	return true
}

// retentionCutoffFor returns the deletion cutoff for one event type.
// (Reserved for per-type sweeps; the current sweeper batches by distinct
// retention duration — see agentInboxService.sweepOncePerType.)
