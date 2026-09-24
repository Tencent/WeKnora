package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrInvalidCatalog marks overlays rejected by validation or compilation.
var ErrInvalidCatalog = errors.New("invalid model catalog")

// MaxCatalogOverlayBytes bounds a console overlay document.
const MaxCatalogOverlayBytes = 1024 * 1024

const catalogHistoryLimit = 20

// CatalogProviderView deliberately omits provider credentials and headers.
type CatalogProviderView struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	API        string             `json:"api"`
	ModelTypes []types.ModelType  `json:"model_types"`
	Settings   map[string]any     `json:"settings"`
	Models     []models.ModelSpec `json:"models"`
	// ModelThinkingLevels lists, per chat model id, the levels offered once
	// thinking is enabled, resolved against the vendor map and protocol so
	// the console does not re-implement level inheritance.
	ModelThinkingLevels map[string][]api.ReasoningEffort `json:"model_thinking_levels"`
	// VendorThinkingLevels is what a chat model without its own map offers.
	VendorThinkingLevels []api.ReasoningEffort `json:"vendor_thinking_levels"`
}

// CatalogRevision is one earlier published overlay.
type CatalogRevision struct {
	Version   uint64          `json:"version"`
	Overlay   json.RawMessage `json:"overlay"`
	UpdatedBy string          `json:"updated_by"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CatalogState is what the console reads: every layer plus the overlay.
type CatalogState struct {
	SyncError       string                `json:"sync_error,omitempty"`
	AppliedVersion  uint64                `json:"applied_version"`
	Version         uint64                `json:"version"`
	Baseline        string                `json:"baseline"`
	Overlay         json.RawMessage       `json:"overlay"`
	History         []CatalogRevision     `json:"history"`
	Builtin         []CatalogProviderView `json:"builtin"`
	Deployment      []CatalogProviderView `json:"deployment"`
	Effective       []CatalogProviderView `json:"effective"`
	DeploymentError string                `json:"deployment_error,omitempty"`
}

// CatalogUpdate is a preview or publish request.
type CatalogUpdate struct {
	Version  uint64          `json:"version"`
	Baseline string          `json:"baseline"`
	Overlay  json.RawMessage `json:"overlay"`
}

// ModelCatalogService persists console overlays independently of the mounted
// deployment file. Every replica re-reads the singleton every five seconds;
// this also repairs missed notifications and works without Redis in Lite mode.
type ModelCatalogService struct {
	repo            *repository.ModelCatalogRepository
	audit           interfaces.AuditLogService
	base            *modelruntime.Runtime
	target          *modelruntime.Runtime
	baseline        string
	deploymentError string
	// Both lower layers are fixed after construction, so their views are too.
	builtinViews    []CatalogProviderView
	deploymentViews []CatalogProviderView
	mu              sync.Mutex
	applied         uint64
	loaded          bool
	// failed remembers a stored version this replica cannot compile, so the
	// poll does not re-read and recompile it until the version changes.
	failed    uint64
	failedErr error
	startOnce sync.Once
}

// NewModelCatalogService loads the deployment layer and applies the stored overlay.
func NewModelCatalogService(
	repo *repository.ModelCatalogRepository, audit interfaces.AuditLogService,
) *ModelCatalogService {
	s := &ModelCatalogService{repo: repo, audit: audit, base: modelruntime.New(), target: modelruntime.Default()}
	data, baseDir, err := modelruntime.ReadDeploymentOverlay(config.ConfigDir())
	if err != nil {
		s.deploymentError = err.Error()
	} else if data != nil {
		if err = s.base.Reload(data, baseDir); err != nil {
			s.deploymentError = err.Error()
		}
	}
	s.builtinViews = catalogProviderViews(modelruntime.New())
	s.deploymentViews = catalogProviderViews(s.base)
	// Include the executable's baseline as well as the file in the preview
	// token, so a rolling upgrade cannot publish against another replica's base.
	builtins, _ := json.Marshal(s.builtinViews)
	hash := sha256.Sum256(append(builtins, data...))
	s.baseline = hex.EncodeToString(hash[:])
	if err := s.Sync(context.Background()); err != nil {
		logger.Warnf(context.Background(), "[model_catalog] initial sync failed: %v", err)
	}
	return s
}

// Start polls for overlays published by other replicas.
func (s *ModelCatalogService) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := s.Sync(ctx); err != nil {
						logger.Warnf(ctx, "[model_catalog] sync failed: %v", err)
					}
				}
			}
		}()
	})
}

// Sync applies the stored overlay if its version changed.
func (s *ModelCatalogService) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	version, err := s.repo.Version(ctx)
	if err != nil {
		return err
	}
	if (s.loaded && version == s.applied) || (s.failedErr != nil && version == s.failed) {
		return nil
	}
	_, err = s.syncLocked(ctx)
	return err
}

func (s *ModelCatalogService) syncLocked(ctx context.Context) (*types.ModelCatalogConfig, error) {
	row, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	if s.loaded && row.Version == s.applied {
		return row, nil
	}
	if s.failedErr != nil && row.Version == s.failed {
		return row, s.failedErr
	}
	overlay, err := validateConsoleOverlay(row.Overlay)
	if err == nil {
		var candidate *modelruntime.Runtime
		if candidate, err = s.base.WithOverlay(overlay, ""); err == nil {
			s.target.RestoreSnapshot(candidate.SnapshotCurrent())
			s.applied, s.loaded, s.failedErr = row.Version, true, nil
			return row, nil
		}
	}
	s.failed, s.failedErr = row.Version, err
	return row, err
}

// State returns the current catalog state for the console.
func (s *ModelCatalogService) State(ctx context.Context) (*CatalogState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.syncLocked(ctx)
	if row == nil {
		return nil, err
	}
	state := s.state(row, s.target)
	if err != nil {
		state.SyncError = err.Error()
	}
	return state, nil
}

func (s *ModelCatalogService) state(row *types.ModelCatalogConfig, runtime *modelruntime.Runtime) *CatalogState {
	history := []CatalogRevision{}
	_ = json.Unmarshal(row.History, &history)
	return &CatalogState{
		Version: row.Version, AppliedVersion: s.applied, Baseline: s.baseline,
		Overlay: json.RawMessage(row.Overlay), History: history,
		Builtin: s.builtinViews, Deployment: s.deploymentViews,
		Effective: catalogProviderViews(runtime), DeploymentError: s.deploymentError,
	}
}

// Preview and Publish use the same compiler. Invalid overlays never reach the
// database or runtime; a stale editor must reload instead of losing changes.
func (s *ModelCatalogService) change(ctx context.Context, req CatalogUpdate, publish bool) (*CatalogState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Recovery must remain possible even if a stored overlay no longer
	// compiles against this deployment baseline after an upgrade.
	row, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	if req.Version != row.Version || req.Baseline != s.baseline {
		return nil, repository.ErrCatalogVersionConflict
	}
	overlay, err := validateConsoleOverlay(req.Overlay)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCatalog, err)
	}
	candidate, err := s.base.WithOverlay(overlay, "")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCatalog, err)
	}
	if !publish {
		// A check only reads the candidate; history and the lower layers are
		// unchanged, so they are left out of the response.
		return &CatalogState{
			Version: row.Version, AppliedVersion: s.applied, Baseline: s.baseline,
			Overlay: json.RawMessage(overlay), Effective: catalogProviderViews(candidate),
			DeploymentError: s.deploymentError,
		}, nil
	}
	history := []CatalogRevision{}
	if err := json.Unmarshal(row.History, &history); err != nil {
		return nil, err
	}
	history = append([]CatalogRevision{{
		Version: row.Version, Overlay: json.RawMessage(row.Overlay),
		UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt,
	}}, history...)
	if len(history) > catalogHistoryLimit {
		history = history[:catalogHistoryLimit]
	}
	rawHistory, _ := json.Marshal(history)
	next := &types.ModelCatalogConfig{
		ID: 1, Version: row.Version + 1, Overlay: types.JSON(overlay),
		History: types.JSON(rawHistory), UpdatedBy: auditActor(ctx), UpdatedAt: time.Now().UTC(),
	}
	if err := s.repo.Save(ctx, row.Version, next); err != nil {
		return nil, err
	}
	s.target.RestoreSnapshot(candidate.SnapshotCurrent())
	s.applied, s.loaded, s.failedErr = next.Version, true, nil
	if s.audit != nil {
		details, _ := json.Marshal(map[string]any{
			"key": "model_catalog", "previous_version": row.Version, "version": next.Version,
		})
		_ = s.audit.Log(ctx, &types.AuditLog{
			TenantID: 0, ActorUserID: auditActor(ctx), ActorRole: "system_admin",
			Action: types.AuditActionSystemSettingChanged, TargetType: "model_catalog", TargetID: "model_catalog",
			Outcome: types.AuditOutcomeSuccess, Details: types.JSON(details),
		})
	}
	return s.state(next, candidate), nil
}

// Preview compiles an overlay without persisting or applying it.
func (s *ModelCatalogService) Preview(ctx context.Context, req CatalogUpdate) (*CatalogState, error) {
	return s.change(ctx, req, false)
}

// Publish persists an overlay as a new version and applies it.
func (s *ModelCatalogService) Publish(ctx context.Context, req CatalogUpdate) (*CatalogState, error) {
	return s.change(ctx, req, true)
}

func catalogProviderViews(runtime *modelruntime.Runtime) []CatalogProviderView {
	out := []CatalogProviderView{}
	for _, p := range runtime.List() {
		view := CatalogProviderView{
			ID: p.ID, Name: p.Name, API: string(p.API), ModelTypes: p.ModelTypes, Models: p.Models(),
			Settings: map[string]any{
				"names": p.Names, "description": p.Description, "descriptions": p.Descriptions,
				// Base URLs are omitted: deployment values may carry
				// interpolated tokens, and the console cannot change them.
				"website": p.Website, "auth": p.Auth,
				"requires_auth": p.RequiresAuth, "url_patterns": p.URLPatterns,
				"compat": p.Compat, "thinking_levels": p.ThinkingLevels,
				"icon_sha256": fmt.Sprintf("%x", sha256.Sum256(p.Icon)),
			},
		}
		view.ModelThinkingLevels = map[string][]api.ReasoningEffort{}
		view.VendorThinkingLevels = []api.ReasoningEffort{}
		// Resolve as if thinking were on: a non-reasoning model reports no
		// levels, which would hide what enabling reasoning would offer.
		enabled := true
		override := &types.ModelSpecOverride{Reasoning: &enabled}
		vendorRef := modelruntime.Ref{Provider: p.ID, Model: "__vendor_default__", Override: override}
		if resolved, err := p.Resolve(vendorRef); err == nil {
			view.VendorThinkingLevels = resolved.Capabilities().ThinkingLevels
		}
		for _, m := range p.ModelsByType(types.ModelTypeKnowledgeQA) {
			if m.ID == "" {
				continue
			}
			ref := modelruntime.Ref{Provider: p.ID, Model: m.ID, Override: override}
			if resolved, err := p.Resolve(ref); err == nil {
				view.ModelThinkingLevels[m.ID] = resolved.Capabilities().ThinkingLevels
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Console documents are portable metadata. Environment expansion, server file
// reads and provider secrets remain deployment-owned. This prevents the editor
// and export/history endpoints becoming environment or credential readers.
func validateConsoleOverlay(raw []byte) ([]byte, error) {
	if len(raw) > MaxCatalogOverlayBytes {
		return nil, fmt.Errorf("catalog overlay exceeds 1 MiB")
	}
	if err := rejectDuplicateCatalogKeys(raw); err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid catalog JSON: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("catalog must be an object with providers")
	}
	delete(root, "_comment") // accepted by the shipped example, never persisted
	for key := range root {
		if key != "providers" {
			return nil, fmt.Errorf("unknown catalog field %q", key)
		}
	}
	var providers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["providers"], &providers); err != nil || providers == nil {
		return nil, fmt.Errorf("providers must be an object")
	}
	seen := map[string]bool{}
	normalizedProviders := map[string]map[string]json.RawMessage{}
	for id, fields := range providers {
		normalized := strings.ToLower(strings.TrimSpace(id))
		if fields == nil || normalized == "" || seen[normalized] {
			return nil, fmt.Errorf("invalid or duplicate provider %q", id)
		}
		seen[normalized] = true
		normalizedProviders[normalized] = fields
		for field := range fields {
			if field != strings.ToLower(field) {
				return nil, fmt.Errorf("provider %s: field names must use lowercase: %s", id, field)
			}
		}
		// Routing fields stay deployment-owned too: moving a vendor's default
		// URL would send its deployment api_key, and the keys of rows without
		// their own base URL, to another host.
		for _, field := range []string{"api_key", "headers", "base_url", "base_urls", "url_patterns", "auth"} {
			if _, exists := fields[field]; exists {
				return nil, fmt.Errorf(
					"provider %s: %s must be managed in deployment configuration or model credentials", id, field)
			}
		}
		if icon, exists := fields["icon"]; exists {
			var value string
			err := json.Unmarshal(icon, &value)
			inline := value == "" || strings.HasPrefix(strings.TrimSpace(value), "<svg")
			if err != nil || !inline {
				return nil, fmt.Errorf("provider %s: console icons must be inline SVG", id)
			}
		}
	}
	// Environment interpolation only applies to the fields rejected above, so
	// a '$' anywhere else (names, descriptions, SVG) is plain text.
	root["providers"], _ = json.Marshal(normalizedProviders)
	canonical, _ := json.Marshal(root)
	return canonical, nil
}

// Duplicate members are ambiguous to operators and different JSON parsers.
func rejectDuplicateCatalogKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid object key")
				}
				if seen[name] {
					return fmt.Errorf("duplicate catalog field %q", name)
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("expected a single JSON document")
	}
	return nil
}
