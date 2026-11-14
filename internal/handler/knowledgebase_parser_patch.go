package handler

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// applyParserRoutingPatch changes only the explicitly named file types. Splitting
// a stored multi-type rule preserves the engine and XLSX options of other types.
func applyParserRoutingPatch(
	cfg *types.ChunkingConfig,
	patch PatchKnowledgeBaseRequest,
	sections map[string]json.RawMessage,
) error {
	if len(patch.ParserFileTypes) == 0 || len(patch.ParserFileTypes) > 20 || patch.ParserEngine == nil {
		return errors.NewBadRequestError(
			"parser_file_types (1-20 extensions) and parser_engine must be provided together",
		)
	}
	var nested map[string]json.RawMessage
	_ = json.Unmarshal(sections["chunking_config"], &nested)
	if _, present := nested["parser_engine_rules"]; present {
		return errors.NewBadRequestError("cannot combine parser_engine_rules with a parser routing patch")
	}
	engine := strings.TrimSpace(*patch.ParserEngine)
	if len(engine) > 100 ||
		strings.IndexFunc(
			engine,
			func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' },
		) >= 0 {
		return errors.NewBadRequestError("invalid parser engine name")
	}
	fileTypes := make([]string, 0, len(patch.ParserFileTypes))
	for _, value := range patch.ParserFileTypes {
		ft := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
		if ft == "" || len(ft) > 20 ||
			strings.IndexFunc(ft, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') }) >= 0 {
			return errors.NewBadRequestError(
				"parser_file_types must contain file extensions, not filenames or wildcards",
			)
		}
		if !slices.Contains(fileTypes, ft) {
			fileTypes = append(fileTypes, ft)
		}
	}
	if patch.ParserXLSXFirstRowAsHeader != nil && (engine == "" || !slices.Contains(fileTypes, "xlsx")) {
		return errors.NewBadRequestError("parser_xlsx_first_row_as_header requires xlsx and a nonempty parser_engine")
	}
	var oldXLSXHeader *bool
	if rule := cfg.ResolveParserEngineRule("xlsx"); rule != nil {
		oldXLSXHeader = rule.XLSXFirstRowAsHeader
	}
	rules := make([]types.ParserEngineRule, 0, len(cfg.ParserEngineRules)+len(fileTypes))
	for _, rule := range cfg.ParserEngineRules {
		kept := make([]string, 0, len(rule.FileTypes))
		for _, ft := range rule.FileTypes {
			if !slices.Contains(fileTypes, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ft)), ".")) {
				kept = append(kept, ft)
			}
		}
		if len(kept) > 0 {
			rule.FileTypes = kept
			rules = append(rules, rule)
		}
	}
	if engine != "" {
		for _, ft := range fileTypes {
			rule := types.ParserEngineRule{FileTypes: []string{ft}, Engine: engine}
			if ft == "xlsx" {
				rule.XLSXFirstRowAsHeader = oldXLSXHeader
				if patch.ParserXLSXFirstRowAsHeader != nil {
					rule.XLSXFirstRowAsHeader = patch.ParserXLSXFirstRowAsHeader
				}
			}
			rules = append(rules, rule)
		}
	}
	cfg.ParserEngineRules = rules
	return nil
}
