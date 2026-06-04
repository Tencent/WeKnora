package tools

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// faqMaxSimilarQuestionsDisplay caps similar_question rows in tool output.
const faqMaxSimilarQuestionsDisplay = 5

func truncateSimilarQuestionsForDisplay(questions []string) (display []string, omitted int) {
	if len(questions) == 0 {
		return nil, 0
	}
	if len(questions) <= faqMaxSimilarQuestionsDisplay {
		return questions, 0
	}
	return questions[:faqMaxSimilarQuestionsDisplay], len(questions) - faqMaxSimilarQuestionsDisplay
}

func writeSimilarQuestionsXML(b *strings.Builder, questions []string) {
	display, omitted := truncateSimilarQuestionsForDisplay(questions)
	for _, sq := range display {
		fmt.Fprintf(b, "<similar_question>%s</similar_question>\n", xmlEscape(sq))
	}
	if omitted > 0 {
		fmt.Fprintf(b, "<similar_questions_omitted count=\"%d\" />\n", omitted)
	}
}

func appendSimilarQuestionsToChunkData(chunkData map[string]interface{}, questions []string) {
	display, omitted := truncateSimilarQuestionsForDisplay(questions)
	if len(display) == 0 {
		return
	}
	chunkData["faq_similar_questions"] = display
	if omitted > 0 {
		chunkData["faq_similar_questions_omitted"] = omitted
	}
}

// writeFAQFieldsXML emits question / similar_question / answer children (no wrapper).
func writeFAQFieldsXML(b *strings.Builder, meta *types.FAQChunkMetadata) {
	if meta == nil {
		return
	}
	if meta.StandardQuestion != "" {
		fmt.Fprintf(b, "<question>%s</question>\n", xmlEscape(meta.StandardQuestion))
	}
	writeSimilarQuestionsXML(b, meta.SimilarQuestions)
	for _, ans := range meta.Answers {
		if strings.TrimSpace(ans) == "" {
			continue
		}
		fmt.Fprintf(b, "<answer>%s</answer>\n", xmlEscape(ans))
	}
}

func faqFieldsEmpty(meta *types.FAQChunkMetadata) bool {
	if meta == nil {
		return true
	}
	return meta.StandardQuestion == "" && len(meta.SimilarQuestions) == 0 && len(meta.Answers) == 0
}

// writeFAQMetadataXML emits a nested <faq> block (used inside knowledge_search <chunk>).
func writeFAQMetadataXML(b *strings.Builder, meta *types.FAQChunkMetadata) {
	if faqFieldsEmpty(meta) {
		return
	}
	b.WriteString("<faq>\n")
	writeFAQFieldsXML(b, meta)
	b.WriteString("</faq>\n")
}

// writeFAQEntryXML emits a top-level FAQ entry for list_knowledge_chunks (not wrapped in <chunk>).
func writeFAQEntryXML(b *strings.Builder, c *types.Chunk) {
	if c == nil || c.ChunkType != types.ChunkTypeFAQ {
		return
	}
	meta, err := c.FAQMetadata()
	if err != nil {
		meta = nil
	}

	q := faqStandardQuestion(c)
	questionAttr := ""
	if q != "" {
		questionAttr = fmt.Sprintf(" question=\"%s\"", xmlEscape(q))
	}
	fmt.Fprintf(b, "<faq faq_id=\"%s\" index=\"%d\"%s>\n",
		xmlEscape(c.ID),
		c.ChunkIndex,
		questionAttr,
	)

	if !faqFieldsEmpty(meta) {
		writeFAQFieldsXML(b, meta)
	} else if q != "" {
		fmt.Fprintf(b, "<question>%s</question>\n", xmlEscape(q))
	}

	b.WriteString("</faq>\n")
}

// normalizeFAQChunkDataMap uses faq_id / index instead of chunk_id / chunk_index in JSON payloads.
func normalizeFAQChunkDataMap(chunkData map[string]interface{}, c *types.Chunk) {
	if c == nil || c.ChunkType != types.ChunkTypeFAQ || chunkData == nil {
		return
	}
	chunkData["faq_id"] = c.ID
	chunkData["index"] = c.ChunkIndex
	delete(chunkData, "chunk_id")
	delete(chunkData, "chunk_index")
}

// appendFAQChunkData adds FAQ metadata fields to structured tool result maps.
func appendFAQChunkData(chunkData map[string]interface{}, c *types.Chunk) {
	if c == nil || c.ChunkType != types.ChunkTypeFAQ {
		return
	}
	meta, err := c.FAQMetadata()
	if err != nil || meta == nil {
		return
	}
	if q := strings.TrimSpace(meta.StandardQuestion); q != "" {
		chunkData["faq_question"] = q
	}
	appendSimilarQuestionsToChunkData(chunkData, meta.SimilarQuestions)
	if len(meta.Answers) > 0 {
		chunkData["faq_answers"] = meta.Answers
	}
}

// Bounds for retrieval-tool match snippets (grep_chunks, knowledge_search).
const (
	snippetContextRunes   = 200
	snippetMaxMatchRunes  = 200
	snippetMaxTotalRunes  = 800
	snippetMaxAnswerRunes = 600
)

// faqMatchSnippetFromQueries builds "Q: … | A: …" for knowledge_search hits.
func faqMatchSnippetFromQueries(meta *types.FAQChunkMetadata, queries []string) string {
	if meta == nil {
		return ""
	}
	question := faqMatchedQuestionFromQueries(meta, queries)
	if question == "" {
		question = strings.TrimSpace(meta.StandardQuestion)
	}
	return formatFAQMatchSnippet(question, meta.Answers)
}

// faqMatchSnippet builds "Q: … | A: …" for grep_chunks regex hits.
func faqMatchSnippet(chunk *types.Chunk, compiled []*regexp.Regexp) string {
	if chunk == nil {
		return ""
	}
	meta, err := chunk.FAQMetadata()
	if err != nil || meta == nil {
		return ""
	}
	question := faqMatchedQuestionFromRegex(meta, compiled)
	if question == "" {
		question = strings.TrimSpace(meta.StandardQuestion)
	}
	if question == "" {
		return ""
	}
	return formatFAQMatchSnippet(question, meta.Answers)
}

func formatFAQMatchSnippet(question string, answers []string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Q: ")
	b.WriteString(question)
	if answer := faqAnswersForSnippet(answers); answer != "" {
		b.WriteString(" | A: ")
		b.WriteString(answer)
	}
	snippet := strings.TrimSpace(b.String())
	if len([]rune(snippet)) > snippetMaxTotalRunes {
		snippet = truncateRunes(snippet, snippetMaxTotalRunes)
	}
	return snippet
}

func faqMatchedQuestionFromRegex(meta *types.FAQChunkMetadata, compiled []*regexp.Regexp) string {
	if meta == nil {
		return ""
	}
	for _, sq := range meta.SimilarQuestions {
		if regexMatchesAny(sq, compiled) {
			return sq
		}
	}
	if regexMatchesAny(meta.StandardQuestion, compiled) {
		return meta.StandardQuestion
	}
	return meta.StandardQuestion
}

func faqMatchedQuestionFromQueries(meta *types.FAQChunkMetadata, queries []string) string {
	if meta == nil {
		return ""
	}
	tokens := searchQueryTokens(queries)
	for _, sq := range meta.SimilarQuestions {
		if textMatchesSearchQueries(sq, queries, tokens) {
			return sq
		}
	}
	if textMatchesSearchQueries(meta.StandardQuestion, queries, tokens) {
		return meta.StandardQuestion
	}
	return meta.StandardQuestion
}

func faqAnswersForSnippet(answers []string) string {
	if len(answers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(answers))
	for _, a := range answers {
		a = strings.TrimSpace(a)
		if a != "" {
			parts = append(parts, a)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateRunes(strings.Join(parts, " | "), snippetMaxAnswerRunes)
}

func textMatchesSearchQueries(text string, queries []string, tokens []string) bool {
	if text == "" {
		return false
	}
	lowered := strings.ToLower(text)
	for _, q := range queries {
		q = strings.ToLower(strings.TrimSpace(q))
		if q != "" && strings.Contains(lowered, q) {
			return true
		}
	}
	for _, tok := range tokens {
		if strings.Contains(lowered, tok) {
			return true
		}
	}
	return false
}

func searchQueryTokens(queries []string) []string {
	tokens := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, q := range queries {
		for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
			switch r {
			case ' ', '\t', '\n', '\r', ',', '.', ';', ':', '?', '!',
				'(', ')', '[', ']', '{', '}', '"', '\'':
				return true
			}
			return false
		}) {
			tok = strings.ToLower(strings.TrimSpace(tok))
			if len([]rune(tok)) < 2 {
				continue
			}
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			tokens = append(tokens, tok)
		}
	}
	return tokens
}
