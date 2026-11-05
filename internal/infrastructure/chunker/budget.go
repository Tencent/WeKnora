// Package chunker - budget.go enforces an explicitly configured token budget
// as a hard upper bound. The character-based estimate is intentionally
// approximate; callers that need exact model tokenization must still validate
// at the embedding boundary.
package chunker

// tokenRuneBudget converts cfg.TokenLimit into a conservative rune budget for
// the configured language. Zero means no explicit budget.
func tokenRuneBudget(cfg SplitterConfig) int {
	if cfg.TokenLimit <= 0 {
		return 0
	}
	lang := LangMixed
	if len(cfg.Languages) > 0 {
		lang = cfg.Languages[0]
	}
	return CharsForTokenLimit(cfg.TokenLimit, lang)
}

// splitOversizeUnits hard-splits source-backed units above limit runes. It is
// used when an explicit token budget is configured because separator
// exhaustion and protected spans can otherwise leave a unit unbounded.
// Offsets stay in source rune space and the concatenated text is unchanged.
func splitOversizeUnits(units []splitUnit, limit int) []splitUnit {
	if limit <= 0 {
		return units
	}
	var out []splitUnit
	for _, u := range units {
		if runeLen(u.text) <= limit {
			out = append(out, u)
			continue
		}
		runes := []rune(u.text)
		for offset := 0; offset < len(runes); {
			end := runeSplitEnd(runes, offset, limit)
			out = append(out, splitUnit{
				text:  string(runes[offset:end]),
				start: u.start + offset,
				end:   u.start + end,
			})
			offset = end
		}
	}
	return out
}

// runeSplitEnd returns the end offset for a piece of at most limit runes
// starting at offset. When the piece would be cut, it prefers the nearest
// newline or space within the trailing 200 runes.
func runeSplitEnd(runes []rune, offset, limit int) int {
	end := offset + limit
	if end >= len(runes) {
		return len(runes)
	}
	for i := end - 1; i > offset && i > end-200; i-- {
		if runes[i] == '\n' || runes[i] == ' ' {
			return i + 1
		}
	}
	return end
}

// enforceChunkBudget splits chunks until the text sent to the embedding model
// (ContextHeader, its "\n\n" separator, any synthetic table header, and the
// chunk body) fits within budget runes. ContextHeader and synthetic table
// headers are dropped only when they alone leave no room for source content.
// Source offsets remain valid for every returned chunk.
func enforceChunkBudget(chunks []Chunk, budget int) []Chunk {
	if budget <= 0 || len(chunks) == 0 {
		return chunks
	}
	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		content := []rune(c.Content)
		headerLen := runeLen(c.ContextHeader)
		overhead := headerLen
		if headerLen > 0 {
			overhead += 2 // EmbeddingContent inserts "\n\n" after the header.
		}
		spanLen := c.End - c.Start
		prefixLen := len(content) - spanLen
		if spanLen <= 0 || prefixLen < 0 {
			// Malformed or synthetic-only chunks are not produced by the
			// splitters; treat their content as source-backed.
			prefixLen = 0
		}

		if overhead+len(content) <= budget {
			c.Seq = len(out)
			out = append(out, c)
			continue
		}

		// Context headers and synthetic table headers are context, not source
		// content. Drop them before sacrificing retrievable text.
		if overhead+prefixLen >= budget {
			c.ContextHeader = ""
			overhead = 0
		}
		if overhead+prefixLen >= budget {
			prefixLen = 0
		}
		bodyBudget := budget - overhead - prefixLen
		if bodyBudget <= 0 {
			c.ContextHeader = ""
			prefixLen = 0
			bodyBudget = budget
		}

		body := content[prefixLen:]
		prefix := string(content[:prefixLen])
		for offset := 0; offset < len(body); {
			end := runeSplitEnd(body, offset, bodyBudget)
			piece := string(body[offset:end])
			if offset == 0 && prefix != "" {
				piece = prefix + piece
			}
			out = append(out, Chunk{
				Content:       piece,
				ContextHeader: c.ContextHeader,
				Seq:           len(out),
				Start:         c.Start + offset,
				End:           c.Start + end,
			})
			offset = end
		}
	}
	return out
}
