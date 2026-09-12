// Command evaluation-provider-probe exercises production Wiki and embedding
// adapters behind a loopback budget relay. It accepts no provider credential.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/buildinfo"
	"github.com/Tencent/WeKnora/internal/modelcache"
	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type request struct {
	Database       string                  `json:"database"`
	BaseURL        string                  `json:"base_url"`
	Step           string                  `json:"step"`
	Arm            string                  `json:"arm"`
	Inputs         []string                `json:"inputs"`
	Data           map[string]string       `json:"data"`
	ChatModel      string                  `json:"chat_model"`
	Messages       []chat.Message          `json:"messages"`
	ChatPrice      types.ModelPriceVersion `json:"chat_price"`
	EmbeddingPrice types.ModelPriceVersion `json:"embedding_price"`
}

type orderedChat struct {
	inner chat.Chat
	arm   string
}

func (c *orderedChat) GetModelName() string { return c.inner.GetModelName() }
func (c *orderedChat) GetModelID() string   { return c.inner.GetModelID() }
func (c *orderedChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("probe is unary only")
}

func (c *orderedChat) Chat(
	ctx context.Context, messages []chat.Message, options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if c.arm == "page-first" {
		messages = append([]chat.Message(nil), messages...)
		for i := range messages {
			text := messages[i].Content
			end := strings.Index(text, "</shared_source_contexts>")
			if messages[i].Role == "user" && strings.HasPrefix(text, "<shared_source_contexts>") && end >= 0 {
				end += len("</shared_source_contexts>")
				messages[i].Content = text[end:] + text[:end]
			}
		}
	}
	copyOptions := *options
	copyOptions.MaxTokens, copyOptions.MaxCompletionTokens = 512, 512
	copyOptions.Temperature = 0
	copyOptions.CacheRetention = chat.CacheRetentionNone
	return c.inner.Chat(ctx, messages, &copyOptions)
}

func run(r request) error {
	if r.ChatModel == "" {
		r.ChatModel = "deepseek/deepseek-v4-flash"
	}
	if r.ChatModel != "deepseek/deepseek-v4-flash" && r.ChatModel != "deepseek/deepseek-v4-pro" &&
		r.ChatModel != "moonshotai/kimi-k2.5" {
		return errors.New("unsupported probe model")
	}
	if r.BaseURL != "http://127.0.0.1:18810/v1" || r.Step == "" {
		return errors.New("fixed loopback relay and step are required")
	}
	if err := os.Setenv("SSRF_WHITELIST", "127.0.0.1"); err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open(r.Database), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&types.ModelCallRecord{}, &types.ModelPriceVersion{},
		&types.EmbeddingCacheEntry{}, &types.EmbeddingCacheLookupRecord{},
	); err != nil {
		return err
	}
	repo := repository.NewModelObservabilityRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	ctx = modelobs.WithEvaluationTask(modelobs.WithPurpose(ctx, modelobs.PurposeEvaluation, true), r.Step)
	chatModel := &types.Model{
		ID: "probe-chat", TenantID: 1, Name: r.ChatModel,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "openrouter", InterfaceType: "openai", BaseURL: r.BaseURL,
			APIKey: "acceptance-relay-placeholder", MaxOutputTokens: 512, ContextWindow: 32768, MaxConcurrency: 1,
		},
	}
	embedModel := &types.Model{
		ID: "probe-embedding", TenantID: 1, Name: "qwen/qwen3-embedding-8b",
		Type: types.ModelTypeEmbedding, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "openrouter", InterfaceType: "openai", BaseURL: r.BaseURL,
			APIKey: "acceptance-relay-placeholder", MaxConcurrency: 1,
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 1024, SupportsDimensionOverride: true},
		},
	}
	prices := map[string]types.ModelPriceVersion{chatModel.ID: r.ChatPrice, embedModel.ID: r.EmbeddingPrice}
	for id, price := range prices {
		current, err := repo.EffectiveModelPrice(ctx, 1, id, time.Now())
		if err != nil {
			return err
		}
		if current == nil {
			price.TenantID, price.ModelID, price.Currency = 1, id, "USD"
			price.ValidFrom = time.Now().UTC().Add(-time.Minute)
			if err := repo.CreateModelPrice(ctx, &price); err != nil {
				return err
			}
		}
	}
	recorder := modelobs.NewRecorder(repo)
	var result any
	if len(r.Inputs) > 0 {
		provider, err := embedding.NewEmbedder(embedding.ConfigFromModel(embedModel, "", ""), nil, nil)
		if err != nil {
			return err
		}
		adapter := modelcache.NewCoordinator(repository.NewEmbeddingCacheRepository(db)).Wrap(
			embedModel, recorder.WrapEmbedder(embedModel, provider),
		)
		result, err = adapter.BatchEmbed(ctx, r.Inputs)
		if err != nil {
			return err
		}
	} else {
		provider, err := chat.NewChat(chat.ConfigFromModel(chatModel, "", ""), nil)
		if err != nil {
			return err
		}
		observed := recorder.WrapChat(chatModel, provider)
		if len(r.Messages) > 0 {
			var response *types.ChatResponse
			response, err = observed.Chat(ctx, r.Messages, &chat.ChatOptions{
				MaxTokens: 512, Temperature: 0, CacheRetention: chat.CacheRetentionNone,
			})
			if response != nil {
				result = response.Content
			}
		} else {
			adapter := &orderedChat{inner: observed, arm: r.Arm}
			result, err = service.NewWikiPageEvaluationRunner(adapter)(ctx, r.Data)
		}
		if err != nil {
			return err
		}
	}
	if err := types.ModelAccountingError(ctx); err != nil {
		return err
	}
	var ledger []types.ModelCallRecord
	if err := db.Where("evaluation_task_id = ?", r.Step).Find(&ledger).Error; err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"step": r.Step, "result": result, "ledger": ledger, "build": buildinfo.Get(),
	})
}

func main() {
	var r request
	if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := run(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
