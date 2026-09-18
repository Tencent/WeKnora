package desensitization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/desensitization/builtin"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrCloudParserForbidden is returned when desensitization is on and a
	// hosted parser would receive the original file.
	ErrCloudParserForbidden = errors.New("cloud parser engines are not allowed when desensitization is enabled")
	// ErrEngineUnavailable is returned when the selected engine cannot run.
	ErrEngineUnavailable = errors.New("desensitization engine is unavailable")
)

// Completer generates a single model completion. Used by the optional LLM engine.
type Completer func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

// Deps wires optional engines. Builtin needs none of these.
type Deps struct {
	PresidioAnalyzerURL   string
	PresidioAnonymizerURL string
	HTTPClient            *http.Client
	Complete              Completer
}

// Engine masks parsed markdown. Implementations must fail closed on errors.
type Engine interface {
	Name() string
	Mask(ctx context.Context, text string, cfg types.DesensitizationConfig) (string, types.JSONMap, error)
}

type builtinEngine struct{}

func (builtinEngine) Name() string { return types.DesensitizationEngineBuiltin }

func (builtinEngine) Mask(ctx context.Context, text string, cfg types.DesensitizationConfig) (string, types.JSONMap, error) {
	return builtin.Mask(ctx, text, cfg)
}

type presidioEngine struct{ deps Deps }

func (presidioEngine) Name() string { return types.DesensitizationEnginePresidio }

func (e presidioEngine) Mask(ctx context.Context, text string, cfg types.DesensitizationConfig) (string, types.JSONMap, error) {
	return maskWithPresidio(ctx, text, cfg, e.deps)
}

type llmEngine struct{ deps Deps }

func (llmEngine) Name() string { return types.DesensitizationEngineLLM }

func (e llmEngine) Mask(ctx context.Context, text string, cfg types.DesensitizationConfig) (string, types.JSONMap, error) {
	return maskWithLLM(ctx, text, cfg, e.deps)
}

// Select picks the engine registered for cfg. Unknown names fall back to builtin
// after Normalize; an empty/disabled config still returns the builtin engine.
func Select(cfg types.DesensitizationConfig, deps Deps) (Engine, error) {
	cfg.Normalize()
	switch cfg.Engine {
	case types.DesensitizationEnginePresidio:
		return presidioEngine{deps: deps}, nil
	case types.DesensitizationEngineLLM:
		return llmEngine{deps: deps}, nil
	case types.DesensitizationEngineBuiltin, "":
		return builtinEngine{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrEngineUnavailable, cfg.Engine)
	}
}

// Apply masks markdown according to cfg. Disabled configs are a no-op.
func Apply(ctx context.Context, text string, cfg types.DesensitizationConfig, deps Deps) (string, types.JSONMap, error) {
	cfg.Normalize()
	if !cfg.Enabled {
		return text, types.JSONMap{"masked": false}, nil
	}
	eng, err := Select(cfg, deps)
	if err != nil {
		return "", nil, err
	}
	return eng.Mask(ctx, text, cfg)
}

// ValidateConfig rejects cloud parsers and invalid custom regexps when masking is on.
func ValidateConfig(cfg *types.DesensitizationConfig, chunking types.ChunkingConfig) error {
	if err := ValidateAgainstParsers(cfg, chunking); err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		return nil
	}
	normalized := *cfg
	normalized.Normalize()
	return builtin.ValidateRules(normalized.Rules)
}

// ValidateAgainstParsers rejects cloud parser rules when masking is enabled.
func ValidateAgainstParsers(cfg *types.DesensitizationConfig, chunking types.ChunkingConfig) error {
	if !cfg.IsEnabled() {
		return nil
	}
	if chunking.HasCloudParserEngine() {
		return ErrCloudParserForbidden
	}
	return nil
}

// ValidateParserEngine rejects a resolved engine name used for one file.
func ValidateParserEngine(cfg *types.DesensitizationConfig, engineName string) error {
	if !cfg.IsEnabled() {
		return nil
	}
	if types.ParserEngineIsCloud(engineName) {
		return ErrCloudParserForbidden
	}
	return nil
}

func placeholder(entity string) string {
	switch entity {
	case types.DesensitizationEntityCNIDCard:
		return "<身份证>"
	case types.DesensitizationEntityCNMobile:
		return "<手机号>"
	case types.DesensitizationEntityCNLandline:
		return "<固定电话>"
	case types.DesensitizationEntityCNBankCard:
		return "<银行卡>"
	case types.DesensitizationEntityCNUSCC:
		return "<统一社会信用代码>"
	case types.DesensitizationEntityCNPlate:
		return "<车牌号>"
	case types.DesensitizationEntityEmail:
		return "<邮箱>"
	default:
		return "<脱敏>"
	}
}

func trimURL(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
