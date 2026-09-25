package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	mcpStartupGrace       = time.Second
	mcpCatalogLoadTimeout = 30 * time.Second
)

// MCPRegisteredTool is a model-visible definition bound to one authorized
// catalog and schema. It must not bypass the directory's live permission and
// configuration checks just because its definition was advertised up front.
type MCPRegisteredTool struct {
	*MCPTool
	catalog *MCPCatalog
	ref     string
}

// Description identifies the external service and original tool.
func (t *MCPRegisteredTool) Description() string {
	return fmt.Sprintf(
		"[MCP service %q, server_id=%q, tool=%q (external)] %s",
		t.service.Name,
		t.service.ID,
		t.mcpTool.Name,
		t.mcpTool.Description,
	)
}

// Execute revalidates the catalog before invoking the bound tool.
func (t *MCPRegisteredTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	tools, _, err := t.catalog.snapshot(ctx, t.service.ID, false)
	if err != nil {
		return mcpDiscoveryFailure(err, "unavailable")
	}
	for _, current := range tools {
		if mcpToolRef(current) != t.ref {
			continue
		}
		if err := t.catalog.checkEnabled(ctx, current); err != nil {
			return mcpDiscoveryFailure(err, "unavailable")
		}
		// Use the latest service configuration, retaining the advertised name for
		// approval/audit records. The registry already validated this exact schema.
		target := NewMCPTool(
			current.service,
			current.mcpTool,
			current.mcpManager,
			current.gate,
			current.authWaitTimeoutSeconds,
		)
		target.registeredName = t.Name()
		return target.Execute(ctx, args)
	}
	return mcpDiscoveryFailure(
		fmt.Errorf("MCP tool definition changed or was removed; rediscover before calling"),
		"unavailable",
	)
}

// mcpRegisteredName is independent of enumeration order and schema revisions.
// Hash raw server/tool identity: sanitizing Unicode or truncating names alone
// silently aliases distinct tools. Keep an ASCII hint for model readability.
func mcpRegisteredName(tool *MCPTool) string {
	sum := sha256.Sum256([]byte(tool.service.ID + "\x00" + tool.mcpTool.Name))
	hint := sanitizeName(tool.service.Name) + "_" + sanitizeName(tool.mcpTool.Name)
	const suffixChars = 16
	const maxHint = maxFunctionNameLength - len("mcp_") - 1 - suffixChars
	if len(hint) > maxHint {
		hint = hint[:maxHint]
	}
	return "mcp_" + hint + "_" + hex.EncodeToString(sum[:suffixChars/2])
}

func (r *ToolRegistry) mcpCatalog() *MCPCatalog {
	if tool, ok := r.tools[ToolDiscoverMCPTools].(*MCPDiscoverTool); ok {
		return tool.catalog
	}
	return nil
}

// PrepareMCPTools advertises sources up front, loading full functions only after
// describe. This is application-level deferred loading via ordinary functions.
// Production readers use persisted metadata and perform no upstream discovery.
func (r *ToolRegistry) PrepareMCPTools(ctx context.Context) {
	r.prepareMCPToolsWithMode(ctx, mcpStartupGrace, false)
}

// PrepareMCPToolsDirect is the full-exposure compatibility path.
func (r *ToolRegistry) PrepareMCPToolsDirect(ctx context.Context) {
	r.prepareMCPTools(ctx, mcpStartupGrace)
}

func (r *ToolRegistry) prepareMCPTools(ctx context.Context, grace time.Duration) {
	r.prepareMCPToolsWithMode(ctx, grace, true)
}

func (r *ToolRegistry) prepareMCPToolsWithMode(ctx context.Context, grace time.Duration, direct bool) {
	c := r.mcpCatalog()
	if c == nil || c.authorize(ctx) != nil {
		return
	}
	r.mcpPrepared, r.mcpDirect = true, direct
	discovery := r.tools[ToolDiscoverMCPTools].(*MCPDiscoverTool)
	discovery.directExposure, discovery.advertiseSources = direct, true
	c.preloadOnce.Do(func() {
		c.preloadDone = make(chan struct{})
		go func() {
			defer close(c.preloadDone)
			loadCtx, cancel := context.WithTimeout(
				context.WithValue(ctx, execCtxKey{}, (*ToolExecContext)(nil)),
				mcpCatalogLoadTimeout,
			)
			defer cancel()
			var workers sync.WaitGroup
			// No ToolExecContext is installed during engine preparation, so this
			// path cannot open an in-conversation OAuth prompt for every service.
			ids := make([]string, 0, len(c.servers))
			for id := range c.servers {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			jobs := make(chan string)
			for i := 0; i < min(8, len(ids)); i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for id := range jobs {
						_, _, _ = c.snapshot(loadCtx, id, false)
					}
				}()
			}
		sendJobs:
			for _, id := range ids {
				select {
				case jobs <- id:
				case <-loadCtx.Done():
					break sendJobs
				}
			}
			close(jobs)
			workers.Wait()
		}()
	})
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.preloadDone:
	case <-timer.C:
	case <-ctx.Done():
	}
	r.RefreshMCPTools(ctx)
}

// RememberMCPHistory republishes tools this session already described or called
// so a new engine does not require another describe round.
func (r *ToolRegistry) RememberMCPHistory(messages []chat.Message) {
	c := r.mcpCatalog()
	if c == nil {
		return
	}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			name := call.Function.Name
			if name == ToolCallMCPTool {
				var args struct {
					ToolRef string `json:"tool_ref"`
				}
				if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.ToolRef != "" {
					c.historyRefs.Store(args.ToolRef, true)
				}
				continue
			}
			if strings.HasPrefix(name, "mcp_") && name != ToolDiscoverMCPTools {
				c.historyNames.Store(name, true)
			}
		}
	}
}

// RefreshMCPTools publishes ready definitions between model requests, including
// catalogs loaded by discovery/OAuth or refreshed after the initial request.
// No network discovery occurs here. Policy checks remain fresh even when the
// metadata snapshot is cached. Call only when parallel tool execution is idle.
func (r *ToolRegistry) RefreshMCPTools(ctx context.Context) {
	if !r.mcpPrepared {
		return
	}
	c := r.mcpCatalog()
	if c == nil {
		return
	}
	// Keep the proxy executable for history, but do not offer an empty call
	// surface to the model. Publish it only alongside a usable full definition.
	r.deferred[ToolCallMCPTool] = true
	for name, tool := range r.tools {
		if _, ok := tool.(*MCPRegisteredTool); ok {
			delete(r.tools, name)
			delete(r.deferred, name)
		}
	}
	if c.authorize(ctx) != nil {
		return
	}
	ids := make([]string, 0, len(c.servers))
	for id := range c.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := c.servers[id]
		entry.mu.Lock()
		service, cached, status := entry.service, entry.tools, entry.status
		entry.mu.Unlock()
		if status != "ready" {
			continue
		}
		if c.lookup != nil {
			current, err := c.lookup(ctx, c.tenantID, id)
			if err != nil || current == nil || current.ID != id || !current.Enabled ||
				!current.UpdatedAt.Equal(service.UpdatedAt) {
				continue
			}
		}
		visible, err := c.visibleTools(ctx, id, cached)
		if err != nil {
			continue
		}
		for _, tool := range visible {
			if !r.mcpDirect && !c.advertised(tool) {
				continue
			}
			c.rememberAdvertised(tool)
			bound := NewMCPTool(tool.service, tool.mcpTool, tool.mcpManager, tool.gate, tool.authWaitTimeoutSeconds)
			bound.registeredName = mcpRegisteredName(tool)
			bound.serverInstructions = tool.serverInstructions
			r.RegisterTool(&MCPRegisteredTool{MCPTool: bound, catalog: c, ref: mcpToolRef(tool)})
			r.deferred[ToolCallMCPTool] = false
		}
	}
}
