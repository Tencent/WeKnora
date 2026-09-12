// Package metricregistry owns versioned evaluation metric registration,
// validation, resolution, and execution. Wire and storage snapshots remain
// owned by internal/types and are produced only through the M3 constructors.
package metricregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Kind identifies which evaluation stage supplies a metric's inputs.
type Kind string

const (
	// KindRetrieval identifies metrics computed from ranked retrieval results.
	KindRetrieval Kind = "retrieval"
	// KindGeneration identifies metrics computed from generated text.
	KindGeneration Kind = "generation"
	// KindEndToEnd identifies metrics computed across the complete evaluation pipeline.
	KindEndToEnd Kind = "end_to_end"
)

var (
	// ErrDuplicateMetric reports a duplicate key and version registration.
	ErrDuplicateMetric = errors.New("duplicate evaluation metric registration")
	// ErrMetricNotFound reports an unresolved key and version.
	ErrMetricNotFound = errors.New("evaluation metric not registered")
	// ErrInvalidMetric reports an invalid definition, configuration, or instance.
	ErrInvalidMetric = errors.New("evaluation metric definition invalid")
	// ErrRequiredMetricFailed reports a required metric compute failure.
	ErrRequiredMetricFailed = errors.New("required evaluation metric failed")
)

// Definition is the public, versioned catalog entry for one algorithm.
type Definition struct {
	Key           string          `json:"key"`
	Version       string          `json:"version"`
	Kind          Kind            `json:"kind"`
	Description   string          `json:"description"`
	DefaultConfig json.RawMessage `json:"default_config"`
	ConfigSchema  json.RawMessage `json:"config_schema"`
}

// Spec requests one configured instance from the registry.
type Spec struct {
	Key      string
	Version  string
	Config   json.RawMessage
	Required bool
}

// Observation is a registry-internal compute result. ResolvedPlan adapts it
// to the frozen M3 observation DTO.
type Observation struct {
	Value     *float64
	Status    string
	ErrorCode string
}

// Metric is the plugin boundary for deterministic and semantic metrics.
type Metric interface {
	Definition() Definition
	Validate(config json.RawMessage) error
	Compute(context.Context, *types.MetricInput, json.RawMessage) (Observation, error)
}

type registeredMetric struct {
	metric Metric
	set    func(*types.MetricResult, float64, json.RawMessage)
}

// Registry is immutable after construction and safe for concurrent reads.
type Registry struct {
	metrics map[string]registeredMetric
}

// New constructs a registry for custom plugins.
func New(metrics ...Metric) (*Registry, error) {
	entries := make([]registeredMetric, 0, len(metrics))
	for _, metric := range metrics {
		entries = append(entries, registeredMetric{metric: metric})
	}
	return newRegistry(entries)
}

func newRegistry(entries []registeredMetric) (*Registry, error) {
	registry := &Registry{metrics: make(map[string]registeredMetric, len(entries))}
	for _, entry := range entries {
		if entry.metric == nil {
			return nil, fmt.Errorf("%w: nil implementation", ErrInvalidMetric)
		}
		definition := entry.metric.Definition()
		if err := validateDefinition(definition); err != nil {
			return nil, err
		}
		key := registryKey(definition.Key, definition.Version)
		if _, exists := registry.metrics[key]; exists {
			return nil, fmt.Errorf("%w: %s@%s", ErrDuplicateMetric, definition.Key, definition.Version)
		}
		registry.metrics[key] = entry
	}
	return registry, nil
}

func validateDefinition(definition Definition) error {
	definition.Key = strings.TrimSpace(definition.Key)
	definition.Version = strings.TrimSpace(definition.Version)
	if definition.Key == "" || definition.Version == "" {
		return fmt.Errorf("%w: key and version are required", ErrInvalidMetric)
	}
	switch definition.Kind {
	case KindRetrieval, KindGeneration, KindEndToEnd:
	default:
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidMetric, definition.Kind)
	}
	for name, value := range map[string]json.RawMessage{
		"default_config": definition.DefaultConfig,
		"config_schema":  definition.ConfigSchema,
	} {
		if len(value) == 0 || !json.Valid(value) {
			return fmt.Errorf("%w: %s must be valid JSON", ErrInvalidMetric, name)
		}
	}
	return nil
}

func registryKey(key, version string) string {
	return strings.TrimSpace(key) + "\x00" + strings.TrimSpace(version)
}

// Definitions returns a stable, detached catalog sorted by key and version.
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	definitions := make([]Definition, 0, len(r.metrics))
	for _, entry := range r.metrics {
		definition := entry.metric.Definition()
		definition.DefaultConfig = append(json.RawMessage(nil), definition.DefaultConfig...)
		definition.ConfigSchema = append(json.RawMessage(nil), definition.ConfigSchema...)
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Key == definitions[j].Key {
			return definitions[i].Version < definitions[j].Version
		}
		return definitions[i].Key < definitions[j].Key
	})
	return definitions
}

// ResolvedPlan pins implementations to the M3 metric plan snapshot.
type ResolvedPlan struct {
	Snapshot *types.EvaluationMetricPlanSnapshot
	entries  map[string]registeredMetric
}

// Resolve validates requested configurations and calls the sole M3 snapshot
// constructors for canonical config, hashes, instance IDs, and plan hash.
func (r *Registry) Resolve(specs []Spec) (*ResolvedPlan, error) {
	if r == nil {
		return nil, errors.New("resolve evaluation metrics: registry is required")
	}
	snapshots := make([]types.EvaluationMetricSpecSnapshot, 0, len(specs))
	entries := make(map[string]registeredMetric, len(specs))
	for _, requested := range specs {
		entry, exists := r.metrics[registryKey(requested.Key, requested.Version)]
		if !exists {
			return nil, fmt.Errorf("%w: %s@%s", ErrMetricNotFound, requested.Key, requested.Version)
		}
		config := requested.Config
		if len(config) == 0 {
			config = entry.metric.Definition().DefaultConfig
		}
		if err := entry.metric.Validate(config); err != nil {
			return nil, fmt.Errorf("validate metric %s@%s: %w", requested.Key, requested.Version, err)
		}
		snapshot, err := types.NewEvaluationMetricSpecSnapshot(
			requested.Key,
			requested.Version,
			config,
			requested.Required,
		)
		if err != nil {
			return nil, fmt.Errorf("snapshot metric %s@%s: %w", requested.Key, requested.Version, err)
		}
		if _, duplicate := entries[snapshot.InstanceID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate instance %s", ErrInvalidMetric, snapshot.InstanceID)
		}
		snapshots = append(snapshots, snapshot)
		entries[snapshot.InstanceID] = entry
	}
	plan, err := types.NewEvaluationMetricPlanSnapshot(snapshots)
	if err != nil {
		return nil, fmt.Errorf("build resolved metric plan: %w", err)
	}
	return &ResolvedPlan{Snapshot: plan, entries: entries}, nil
}

// ResolveSnapshot rebinds a persisted M3 plan to the registered
// implementations and rejects any canonical mismatch.
func (r *Registry) ResolveSnapshot(snapshot *types.EvaluationMetricPlanSnapshot) (*ResolvedPlan, error) {
	if snapshot == nil {
		return nil, errors.New("resolve evaluation metrics: snapshot is required")
	}
	specs := make([]Spec, 0, len(snapshot.Metrics))
	for _, item := range snapshot.Metrics {
		specs = append(specs, Spec{
			Key: item.Key, Version: item.Version, Config: item.Config, Required: item.Required,
		})
	}
	resolved, err := r.Resolve(specs)
	if err != nil {
		return nil, err
	}
	if resolved.Snapshot.PlanHash != snapshot.PlanHash {
		return nil, fmt.Errorf("resolve evaluation metrics: plan hash mismatch: stored %s, resolved %s",
			snapshot.PlanHash, resolved.Snapshot.PlanHash)
	}
	return resolved, nil
}

// Compute executes the frozen instances in snapshot order. Optional failures
// remain failed observations; a required failure stops the sample and task.
func (p *ResolvedPlan) Compute(
	ctx context.Context,
	input *types.MetricInput,
) (*types.MetricResult, []types.EvaluationMetricObservationSnapshot, error) {
	if p == nil || p.Snapshot == nil {
		return nil, nil, errors.New("compute evaluation metrics: resolved plan is required")
	}
	result := &types.MetricResult{Scores: make(map[string]types.EvaluationMetricScore, len(p.Snapshot.Metrics))}
	observations := make([]types.EvaluationMetricObservationSnapshot, 0, len(p.Snapshot.Metrics))
	for _, spec := range p.Snapshot.Metrics {
		if err := ctx.Err(); err != nil {
			return result, observations, err
		}
		entry := p.entries[spec.InstanceID]
		observation, computeErr := entry.metric.Compute(ctx, input, spec.Config)
		wire := types.EvaluationMetricObservationSnapshot{
			InstanceID: spec.InstanceID,
			Value:      observation.Value,
			Status:     observation.Status,
			ErrorCode:  observation.ErrorCode,
		}
		if wire.Status == "" {
			if computeErr == nil && wire.Value != nil {
				wire.Status = types.EvaluationMetricObservationValid
			} else {
				wire.Status = types.EvaluationMetricObservationFailed
			}
		}
		if computeErr != nil && wire.ErrorCode == "" {
			wire.ErrorCode = "metric_compute_failed"
		}
		if wire.Status == types.EvaluationMetricObservationValid && wire.Value != nil {
			result.Scores[spec.InstanceID] = types.EvaluationMetricScore{
				Value: wire.Value, Status: wire.Status,
			}
			if entry.set != nil {
				entry.set(result, *wire.Value, spec.Config)
			}
		} else {
			result.Scores[spec.InstanceID] = types.EvaluationMetricScore{
				Value: wire.Value, Status: wire.Status, ErrorCode: wire.ErrorCode,
			}
		}
		observations = append(observations, wire)
		if computeErr != nil && spec.Required {
			return result, observations, fmt.Errorf(
				"%w: %s (%s): %v",
				ErrRequiredMetricFailed,
				spec.InstanceID,
				wire.ErrorCode,
				computeErr,
			)
		}
	}
	return result, observations, nil
}

// Aggregate computes deterministic means from per-sample registry results.
// It iterates the frozen plan order rather than worker completion order.
func (p *ResolvedPlan) Aggregate(results []*types.MetricResult) *types.MetricResult {
	aggregate := &types.MetricResult{Scores: make(map[string]types.EvaluationMetricScore, len(p.Snapshot.Metrics))}
	for _, spec := range p.Snapshot.Metrics {
		var sum float64
		var count int
		status := types.EvaluationMetricObservationSkipped
		errorCode := "insufficient_valid_samples"
		for _, result := range results {
			if result == nil {
				continue
			}
			score, exists := result.Scores[spec.InstanceID]
			if !exists {
				continue
			}
			if score.Status == types.EvaluationMetricObservationValid && score.Value != nil {
				sum += *score.Value
				count++
				continue
			}
			if status == types.EvaluationMetricObservationSkipped {
				status = score.Status
				errorCode = score.ErrorCode
			}
		}
		if count == 0 {
			aggregate.Scores[spec.InstanceID] = types.EvaluationMetricScore{
				Status: status, ErrorCode: errorCode,
			}
			continue
		}
		value := sum / float64(count)
		aggregate.Scores[spec.InstanceID] = types.EvaluationMetricScore{
			Value: &value, Status: types.EvaluationMetricObservationValid,
		}
		if entry := p.entries[spec.InstanceID]; entry.set != nil {
			entry.set(aggregate, value, spec.Config)
		}
	}
	return aggregate
}
