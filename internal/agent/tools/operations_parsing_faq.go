package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/operations"
)

// parsingAndFAQDetail keeps the actual change visible on the approval card.
func parsingAndFAQDetail(op operations.Operation, params map[string]json.RawMessage) []string {
	if op.ID != "knowledge_base.update" && op.ID != "faq.batch_update" {
		return nil
	}
	var lines []string
	for _, field := range []struct{ key, label string }{
		{"is_enabled", "Enabled"},
		{"is_recommended", "Recommended"},
		{"enable_parent_child", "Parent/child chunking"},
		{"vlm_enabled", "Image processing"},
		{"image_caption_enabled", "Image captions"},
		{"image_ocr_enabled", "OCR"},
		{"question_generation_enabled", "Question generation"},
		{"parser_xlsx_first_row_as_header", "XLSX first row as header"},
	} {
		if value, ok := rawBool(params, field.key); ok {
			lines = append(lines, fmt.Sprintf("%s: %t", field.label, value))
		}
	}
	for _, field := range []struct{ key, label string }{
		{"chunk_size", "Chunk size"},
		{"chunk_overlap", "Chunk overlap"},
		{"parent_chunk_size", "Parent chunk size"},
		{"child_chunk_size", "Child chunk size"},
		{"question_count", "Questions per chunk"},
		{"tag_seq_id", "Tag seq_id (0 clears)"},
	} {
		if value, ok := rawInt(params, field.key); ok {
			lines = append(lines, fmt.Sprintf("%s: %d", field.label, value))
		}
	}
	for _, field := range []struct{ key, label string }{
		{
			"strategy",
			"Chunking strategy",
		},
		{
			"image_pipeline",
			"Image pipeline",
		},
		{
			"parser_engine",
			"Parser engine (empty restores defaults)",
		},
	} {
		if value, ok := rawString(params, field.key); ok {
			lines = append(lines, field.label+": "+value)
		}
	}
	if fileTypes, ok := rawStringList(params, "parser_file_types"); ok {
		lines = append(lines, "File types: "+strings.Join(fileTypes, ", "))
	}
	if op.ID == "knowledge_base.update" && len(lines) > 0 {
		lines = append(
			lines,
			"Parsing changes apply to future parses. Existing documents are not automatically reparsed.",
		)
	}
	return lines
}
