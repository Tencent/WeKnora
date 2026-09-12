package metricregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type builtinMetric struct {
	definition              Definition
	validate                func(json.RawMessage) error
	calculator              interfaces.Metrics
	requiresRetrievalLabels bool
}

func (m builtinMetric) Definition() Definition { return m.definition }

func (m builtinMetric) Validate(config json.RawMessage) error {
	if m.validate == nil {
		return validateEmptyObject(config)
	}
	return m.validate(config)
}

func (m builtinMetric) Compute(
	ctx context.Context,
	input *types.MetricInput,
	_ json.RawMessage,
) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "context_canceled"}, err
	}
	if m.requiresRetrievalLabels && (input == nil || !input.RetrievalLabelsAvailable) {
		return Observation{Status: types.EvaluationMetricObservationMissing, ErrorCode: "relevance_labels_missing"}, nil
	}
	value := m.calculator.Compute(input)
	return Observation{Value: &value, Status: types.EvaluationMetricObservationValid}, nil
}

type configuredMetric struct {
	definition              Definition
	validate                func(json.RawMessage) error
	build                   func(json.RawMessage) (interfaces.Metrics, error)
	requiresRetrievalLabels bool
}

func (m configuredMetric) Definition() Definition { return m.definition }

func (m configuredMetric) Validate(config json.RawMessage) error { return m.validate(config) }

func (m configuredMetric) Compute(
	ctx context.Context,
	input *types.MetricInput,
	config json.RawMessage,
) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "context_canceled"}, err
	}
	if m.requiresRetrievalLabels && (input == nil || !input.RetrievalLabelsAvailable) {
		return Observation{Status: types.EvaluationMetricObservationMissing, ErrorCode: "relevance_labels_missing"}, nil
	}
	calculator, err := m.build(config)
	if err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "invalid_metric_config"}, err
	}
	value := calculator.Compute(input)
	return Observation{Value: &value, Status: types.EvaluationMetricObservationValid}, nil
}

var emptyObjectSchema = json.RawMessage(`{"type":"object","additionalProperties":false}`)

// NewDefaultRegistry constructs the current algorithm catalog. Returning an
// error makes invalid or duplicate registrations a container startup error.
func NewDefaultRegistry() (*Registry, error) {
	return NewDefaultRegistryWithPlugins()
}

// NewDefaultRegistryWithPlugins combines the compatibility algorithms and the
// current graded-relevance algorithms with opt-in plugins.
func NewDefaultRegistryWithPlugins(plugins ...Metric) (*Registry, error) {
	entries := []registeredMetric{
		builtinEntry(
			"retrieval.precision", KindRetrieval, "Fraction of retrieved ranks that are relevant.",
			metric.NewPrecisionMetric(),
			func(result *types.MetricResult, value float64) { result.RetrievalMetrics.Precision = value },
		),
		builtinEntry(
			"retrieval.recall", KindRetrieval, "Fraction of relevant passages retrieved.",
			metric.NewRecallMetric(),
			func(result *types.MetricResult, value float64) { result.RetrievalMetrics.Recall = value },
		),
		configuredEntry(
			Definition{
				Key: "retrieval.ndcg", Version: "1.0.0", Kind: KindRetrieval,
				Description:   "Normalized discounted cumulative gain at a configured cutoff.",
				DefaultConfig: json.RawMessage(`{"k":3}`),
				ConfigSchema: json.RawMessage(
					`{"type":"object","required":["k"],` +
						`"properties":{"k":{"type":"integer","minimum":1}},"additionalProperties":false}`,
				),
			},
			validatePositiveInt("k"),
			func(config json.RawMessage) (interfaces.Metrics, error) {
				value, err := intConfig(config, "k")
				return metric.NewNDCGMetric(value), err
			},
			func(result *types.MetricResult, value float64, config json.RawMessage) {
				cutoff, _ := intConfig(config, "k")
				switch cutoff {
				case 3:
					result.RetrievalMetrics.NDCG3 = value
				case 10:
					result.RetrievalMetrics.NDCG10 = value
				}
			},
		),
		configuredEntryWithLabelRequirement(
			Definition{
				Key: "retrieval.ndcg", Version: "2.0.0", Kind: KindRetrieval,
				Description:   "Graded normalized discounted cumulative gain at a configured cutoff.",
				DefaultConfig: json.RawMessage(`{"k":3}`),
				ConfigSchema: json.RawMessage(
					`{"type":"object","required":["k"],` +
						`"properties":{"k":{"type":"integer","minimum":1}},"additionalProperties":false}`,
				),
			},
			validatePositiveInt("k"),
			func(config json.RawMessage) (interfaces.Metrics, error) {
				value, err := intConfig(config, "k")
				return metric.NewNDCGMetricV2(value), err
			},
			func(result *types.MetricResult, value float64, config json.RawMessage) {
				cutoff, _ := intConfig(config, "k")
				switch cutoff {
				case 3:
					result.RetrievalMetrics.NDCG3 = value
				case 10:
					result.RetrievalMetrics.NDCG10 = value
				}
			},
		),
		builtinEntry(
			"retrieval.mrr", KindRetrieval, "Reciprocal rank of the first relevant passage.",
			metric.NewMRRMetric(),
			func(result *types.MetricResult, value float64) { result.RetrievalMetrics.MRR = value },
		),
		builtinVersionedEntry(
			"retrieval.map", "2.0.0", KindRetrieval,
			"Binary mean average precision normalized by the complete relevant set.",
			metric.NewMAPMetricV2(), true,
			func(result *types.MetricResult, value float64) { result.RetrievalMetrics.MAP = value },
		),
		builtinEntry(
			"retrieval.map", KindRetrieval, "Mean average precision across relevance sets.",
			metric.NewMAPMetric(),
			func(result *types.MetricResult, value float64) { result.RetrievalMetrics.MAP = value },
		),
		configuredEntry(
			Definition{
				Key: "generation.bleu", Version: "1.0.0", Kind: KindGeneration,
				Description:   "Bilingual Evaluation Understudy score at a configured n-gram order.",
				DefaultConfig: json.RawMessage(`{"n":1}`),
				ConfigSchema: json.RawMessage(
					`{"type":"object","required":["n"],` +
						`"properties":{"n":{"type":"integer","enum":[1,2,4]}},"additionalProperties":false}`,
				),
			},
			validateEnumInt("n", 1, 2, 4),
			func(config json.RawMessage) (interfaces.Metrics, error) {
				order, err := intConfig(config, "n")
				if err != nil {
					return nil, err
				}
				weights := metric.BLEU1Gram
				switch order {
				case 2:
					weights = metric.BLEU2Gram
				case 4:
					weights = metric.BLEU4Gram
				}
				return metric.NewBLEUMetric(true, weights), nil
			},
			func(result *types.MetricResult, value float64, config json.RawMessage) {
				order, _ := intConfig(config, "n")
				switch order {
				case 1:
					result.GenerationMetrics.BLEU1 = value
				case 2:
					result.GenerationMetrics.BLEU2 = value
				case 4:
					result.GenerationMetrics.BLEU4 = value
				}
			},
		),
		configuredEntry(
			Definition{
				Key: "generation.rouge", Version: "1.0.0", Kind: KindGeneration,
				Description:   "Recall-Oriented Understudy for Gisting Evaluation F-score.",
				DefaultConfig: json.RawMessage(`{"variant":"rouge-1"}`),
				ConfigSchema: json.RawMessage(
					`{"type":"object","required":["variant"],` +
						`"properties":{"variant":{"type":"string",` +
						`"enum":["rouge-1","rouge-2","rouge-l"]}},"additionalProperties":false}`,
				),
			},
			validateEnumString("variant", "rouge-1", "rouge-2", "rouge-l"),
			func(config json.RawMessage) (interfaces.Metrics, error) {
				variant, err := stringConfig(config, "variant")
				return metric.NewRougeMetric(true, variant, "f"), err
			},
			func(result *types.MetricResult, value float64, config json.RawMessage) {
				variant, _ := stringConfig(config, "variant")
				switch variant {
				case "rouge-1":
					result.GenerationMetrics.ROUGE1 = value
				case "rouge-2":
					result.GenerationMetrics.ROUGE2 = value
				case "rouge-l":
					result.GenerationMetrics.ROUGEL = value
				}
			},
		),
	}
	for _, plugin := range plugins {
		entries = append(entries, registeredMetric{metric: plugin})
	}
	return newRegistry(entries)
}

func builtinEntry(
	key string,
	kind Kind,
	description string,
	calculator interfaces.Metrics,
	set func(*types.MetricResult, float64),
) registeredMetric {
	return builtinVersionedEntry(key, "1.0.0", kind, description, calculator, false, set)
}

func builtinVersionedEntry(
	key string,
	version string,
	kind Kind,
	description string,
	calculator interfaces.Metrics,
	requiresRetrievalLabels bool,
	set func(*types.MetricResult, float64),
) registeredMetric {
	return registeredMetric{
		metric: builtinMetric{
			definition: Definition{
				Key: key, Version: version, Kind: kind, Description: description,
				DefaultConfig: json.RawMessage(`{}`), ConfigSchema: emptyObjectSchema,
			},
			calculator: calculator, requiresRetrievalLabels: requiresRetrievalLabels,
		},
		set: func(result *types.MetricResult, value float64, _ json.RawMessage) { set(result, value) },
	}
}

func configuredEntryWithLabelRequirement(
	definition Definition,
	validate func(json.RawMessage) error,
	build func(json.RawMessage) (interfaces.Metrics, error),
	set func(*types.MetricResult, float64, json.RawMessage),
) registeredMetric {
	return registeredMetric{
		metric: configuredMetric{
			definition: definition, validate: validate, build: build, requiresRetrievalLabels: true,
		},
		set: set,
	}
}

func configuredEntry(
	definition Definition,
	validate func(json.RawMessage) error,
	build func(json.RawMessage) (interfaces.Metrics, error),
	set func(*types.MetricResult, float64, json.RawMessage),
) registeredMetric {
	return registeredMetric{
		metric: configuredMetric{definition: definition, validate: validate, build: build},
		set:    set,
	}
}

// DefaultSpecs is the stable twelve-instance compatibility plan.
func DefaultSpecs() []Spec {
	return []Spec{
		{Key: "retrieval.precision", Version: "1.0.0", Required: true},
		{Key: "retrieval.recall", Version: "1.0.0", Required: true},
		{Key: "retrieval.ndcg", Version: "2.0.0", Config: json.RawMessage(`{"k":3}`), Required: true},
		{Key: "retrieval.ndcg", Version: "2.0.0", Config: json.RawMessage(`{"k":10}`), Required: true},
		{Key: "retrieval.mrr", Version: "1.0.0", Required: true},
		{Key: "retrieval.map", Version: "2.0.0", Required: true},
		{Key: "generation.bleu", Version: "1.0.0", Config: json.RawMessage(`{"n":1}`), Required: true},
		{Key: "generation.bleu", Version: "1.0.0", Config: json.RawMessage(`{"n":2}`), Required: true},
		{Key: "generation.bleu", Version: "1.0.0", Config: json.RawMessage(`{"n":4}`), Required: true},
		{Key: "generation.rouge", Version: "1.0.0", Config: json.RawMessage(`{"variant":"rouge-1"}`), Required: true},
		{Key: "generation.rouge", Version: "1.0.0", Config: json.RawMessage(`{"variant":"rouge-2"}`), Required: true},
		{Key: "generation.rouge", Version: "1.0.0", Config: json.RawMessage(`{"variant":"rouge-l"}`), Required: true},
	}
}

func validateEmptyObject(config json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(config, &value); err != nil {
		return err
	}
	if len(value) != 0 {
		return errors.New("configuration must be an empty object")
	}
	return nil
}

func validatePositiveInt(field string) func(json.RawMessage) error {
	return func(config json.RawMessage) error {
		value, err := intConfig(config, field)
		if err != nil {
			return err
		}
		if value < 1 {
			return fmt.Errorf("%s must be at least 1", field)
		}
		return nil
	}
}

func validateEnumInt(field string, allowed ...int) func(json.RawMessage) error {
	return func(config json.RawMessage) error {
		value, err := intConfig(config, field)
		if err != nil {
			return err
		}
		for _, item := range allowed {
			if value == item {
				return nil
			}
		}
		return fmt.Errorf("%s has unsupported value %d", field, value)
	}
}

func validateEnumString(field string, allowed ...string) func(json.RawMessage) error {
	return func(config json.RawMessage) error {
		value, err := stringConfig(config, field)
		if err != nil {
			return err
		}
		for _, item := range allowed {
			if value == item {
				return nil
			}
		}
		return fmt.Errorf("%s has unsupported value %q", field, value)
	}
}

func intConfig(config json.RawMessage, field string) (int, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return 0, err
	}
	if len(object) != 1 {
		return 0, errors.New("configuration contains unknown fields")
	}
	var value int
	if err := json.Unmarshal(object[field], &value); err != nil {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	return value, nil
}

func stringConfig(config json.RawMessage, field string) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return "", err
	}
	if len(object) != 1 {
		return "", errors.New("configuration contains unknown fields")
	}
	var value string
	if err := json.Unmarshal(object[field], &value); err != nil || value == "" {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	return value, nil
}
