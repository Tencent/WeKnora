// Prompt template DB integration.
//
// Lifecycle:
//
//  1. LoadConfig() reads YAML files from config/prompt_templates/*.yaml into
//     cfg.PromptTemplates (existing behaviour, untouched).
//  2. After the GORM DB is ready, container.go calls
//     SeedAndLoadPromptTemplates(ctx, cfg, repo). That function:
//       a. Inserts every YAML template missing from the DB (by composite key).
//          → first boot fills the table; subsequent boots only top-up new
//            built-ins added by a release.
//       b. Reads the (now full) DB back, and replaces cfg.PromptTemplates with
//          the DB-sourced version. The DB is the single source of truth from
//          this point on.
//       c. Re-runs backfillConversationDefaults so cfg.Conversation fields
//          (Summary.Prompt, FallbackPrompt, RewritePromptSystem, …) reflect
//          the DB content rather than the YAML snapshot.
//
// Failure mode: if the DB is unreachable or the seed/load fails, the function
// logs a warning and leaves cfg.PromptTemplates untouched (still YAML-backed).
// The service continues to start — prompts simply remain read-only until the
// DB recovers.
package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SeedAndLoadPromptTemplates is the entry point invoked by the container after
// the database is ready. See file-level doc for behaviour.
func SeedAndLoadPromptTemplates(
	ctx context.Context,
	cfg *Config,
	repo interfaces.PromptTemplateRepository,
) error {
	if cfg == nil || repo == nil {
		return errors.New("prompt_templates: nil cfg or repo")
	}
	if cfg.PromptTemplates == nil {
		// No YAML loaded — nothing to seed and nothing to merge. The DB
		// might still hold rows from a previous deployment though, so try
		// loading.
		cfg.PromptTemplates = &PromptTemplatesConfig{}
	}

	// 1. Seed any YAML rows that aren't yet in the DB.
	if err := seedYAMLToDB(ctx, cfg.PromptTemplates, repo); err != nil {
		return fmt.Errorf("seed prompt templates: %w", err)
	}

	// 2. Pull the (now full) DB back into cfg.PromptTemplates and refresh
	//    cfg.Conversation defaults that reference template IDs.
	return RefreshPromptTemplatesFromDB(ctx, cfg, repo)
}

// RefreshPromptTemplatesFromDB reloads the user-facing categories from the
// DB into cfg.PromptTemplates and re-resolves the conversation defaults that
// reference template IDs. Call after every Upsert/Delete to make changes
// visible to runtime code without restarting.
//
// Mutation semantics: replaces the per-category slices on cfg.PromptTemplates
// (which is a *PromptTemplatesConfig, shared by reference across services),
// and updates cfg.Conversation.* fields in place. No locking is performed —
// see backfillConversationDefaults for the existing assumption that string
// field assignment is acceptable here.
func RefreshPromptTemplatesFromDB(
	ctx context.Context,
	cfg *Config,
	repo interfaces.PromptTemplateRepository,
) error {
	if cfg == nil || repo == nil {
		return errors.New("prompt_templates: nil cfg or repo")
	}
	if cfg.PromptTemplates == nil {
		cfg.PromptTemplates = &PromptTemplatesConfig{}
	}

	rows, err := repo.List(ctx)
	if err != nil {
		return fmt.Errorf("list prompt templates from DB: %w", err)
	}
	mergePromptTemplatesFromDB(cfg.PromptTemplates, rows)

	if cfg.Conversation != nil {
		backfillConversationDefaults(cfg)
	}

	// Builtin agents (quick-answer / data-analyst / wiki-researcher /
	// wiki-fixer) cache the resolved system_prompt / context_template content
	// on their CustomAgentConfig at startup. That cache is what
	// `GET /api/v1/agents/<builtin-id>` returns to the frontend, so without
	// invalidating it the UI keeps showing the YAML-era text after we swap
	// in DB content. resolveBuiltinAgentPromptIDs (defined in config.go)
	// internally resets and re-resolves, so calling it again here is safe
	// and idempotent.
	resolveBuiltinAgentPromptIDs(cfg.PromptTemplates)
	return nil
}

// seedYAMLToDB inserts every YAML template that is not already present in the
// DB. Rows already present (regardless of whether the user modified them) are
// left untouched.
func seedYAMLToDB(
	ctx context.Context,
	pt *PromptTemplatesConfig,
	repo interfaces.PromptTemplateRepository,
) error {
	existing, err := repo.ExistingIDs(ctx)
	if err != nil {
		return fmt.Errorf("query existing prompt template IDs: %w", err)
	}

	// Visit every category we own. ranges are intentionally written out so
	// the compiler catches us if a new category constant is added without
	// updating this list.
	type yamlSource struct {
		category  string
		templates []PromptTemplate
	}
	sources := []yamlSource{
		{types.PromptTemplateCategorySystemPrompt, pt.SystemPrompt},
		{types.PromptTemplateCategoryAgentSystemPrompt, pt.AgentSystemPrompt},
		{types.PromptTemplateCategoryContextTemplate, pt.ContextTemplate},
		{types.PromptTemplateCategoryRewrite, pt.Rewrite},
		{types.PromptTemplateCategoryFallback, pt.Fallback},
	}

	for _, src := range sources {
		bucket := existing[src.category]
		for i := range src.templates {
			tmpl := &src.templates[i]
			if tmpl.ID == "" {
				continue
			}
			if _, ok := bucket[tmpl.ID]; ok {
				continue // user/previous-run already owns this row
			}
			rec := promptTemplateToRecord(src.category, tmpl)
			if err := repo.Upsert(ctx, rec); err != nil {
				return fmt.Errorf("seed prompt template %s/%s: %w",
					src.category, tmpl.ID, err)
			}
		}
	}
	return nil
}

// mergePromptTemplatesFromDB replaces every category-list inside pt with the
// content read from the DB. Other fields of pt (intent_prompts,
// generate_summary, …) are left alone — they still come from YAML.
func mergePromptTemplatesFromDB(pt *PromptTemplatesConfig, rows []*types.PromptTemplateRecord) {
	if pt == nil {
		return
	}

	// Bucket rows by category for predictable iteration order.
	buckets := make(map[string][]PromptTemplate, len(types.AllPromptTemplateCategories))
	for _, r := range rows {
		if r == nil {
			continue
		}
		buckets[r.Category] = append(buckets[r.Category], recordToPromptTemplate(r))
	}

	pt.SystemPrompt = buckets[types.PromptTemplateCategorySystemPrompt]
	pt.AgentSystemPrompt = buckets[types.PromptTemplateCategoryAgentSystemPrompt]
	pt.ContextTemplate = buckets[types.PromptTemplateCategoryContextTemplate]
	pt.Rewrite = buckets[types.PromptTemplateCategoryRewrite]
	pt.Fallback = buckets[types.PromptTemplateCategoryFallback]
}

// promptTemplateToRecord projects a YAML-loaded PromptTemplate into the GORM
// record shape expected by the DB.
func promptTemplateToRecord(category string, t *PromptTemplate) *types.PromptTemplateRecord {
	rec := &types.PromptTemplateRecord{
		Category:     category,
		ID:           t.ID,
		Name:         t.Name,
		Description:  t.Description,
		Content:      t.Content,
		UserPrompt:   t.User,
		HasKB:        t.HasKnowledgeBase,
		HasWebSearch: t.HasWebSearch,
		IsDefault:    t.Default,
		Mode:         t.Mode,
	}
	if len(t.I18n) > 0 {
		rec.I18n = make(types.PromptTemplateI18nMap, len(t.I18n))
		for locale, entry := range t.I18n {
			rec.I18n[locale] = types.PromptTemplateI18nEntry{
				Name:        entry.Name,
				Description: entry.Description,
			}
		}
	}
	return rec
}

// recordToPromptTemplate is the inverse projection used when loading the DB
// back into cfg.PromptTemplates.
func recordToPromptTemplate(r *types.PromptTemplateRecord) PromptTemplate {
	out := PromptTemplate{
		ID:               r.ID,
		Name:             r.Name,
		Description:      r.Description,
		Content:          r.Content,
		User:             r.UserPrompt,
		HasKnowledgeBase: r.HasKB,
		HasWebSearch:     r.HasWebSearch,
		Default:          r.IsDefault,
		Mode:             r.Mode,
	}
	if len(r.I18n) > 0 {
		out.I18n = make(map[string]PromptTemplateI18n, len(r.I18n))
		for locale, entry := range r.I18n {
			out.I18n[locale] = PromptTemplateI18n{
				Name:        entry.Name,
				Description: entry.Description,
			}
		}
	}
	return out
}
