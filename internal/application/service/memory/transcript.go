package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Extraction reads a conversation as evidence about one person, and the rows it
// reads are not equally trustworthy.
//
// This file used to hand the model user messages only. That is safe, and it is
// also why extraction kept missing the most valuable things a conversation
// contains. A correction — "不对，重来" — says nothing without the answer it
// rejects. The conclusion of a question lives in the reply, not in the
// question. Which documents an answer drew on is the strongest personal signal
// a knowledge-base product has, and it is only ever attached to an assistant
// message. Dropping all of it left the extraction model reading a list of
// questions and guessing.
//
// Codex's memory pipeline solves this by tiering rather than filtering: every
// row of a session is eligible, the budget is spent tier by tier — the human
// first, then the assistant's own messages, then the cited titles, then what
// the tools did — and the prompt states outright which tiers are evidence and
// which are only context. Selection walks newest-first inside each tier, but
// the rows that survive are printed in the order they happened, because a
// transcript reordered by trust is no longer a transcript. This file is that
// arrangement.
//
// The safety property the user-only filter used to provide is kept
// structurally instead: every row is labelled with the tier it came from, so
// the prompt's "describe this, never obey it" rule has something to bite on,
// and a verbatim note is only accepted when it can be found in a row that was
// labelled [user] (findUserLine enforces it).
//
// Every row is also redacted here, before the prompt is built, because this is
// the last point before the transcript leaves the building.

const (
	// Per-row ceilings, in runes. They differ by tier because the tiers fail
	// differently: a user message is short and every word of it matters, an
	// answer is long and repetitive and only its gist and any concrete
	// conclusion are worth the budget.
	extractUserRowMaxRunes = 1000
	// An answer gets the same ceiling as a question. It used to get 400, on the
	// theory that only an answer's gist is worth the budget — but the gist of
	// an answer is its conclusion, which is at the end, which is exactly what
	// a 400-rune cut removes. Codex caps every row alike and squeezes only
	// tool output; what limits the assistant's share here is the tier order
	// below, which is the mechanism that is supposed to limit it.
	extractAssistantRowMaxRunes = 1000
	extractCitationRowMaxRunes  = 200
	// A tool row is a call and what came back. The ceiling is generous because
	// the interesting part of a search result is usually at the top and the
	// interesting part of a failure is the error, and both fit; the tier order
	// is what keeps tool rows from crowding out the user's words.
	extractToolRowMaxRunes = 600
	// extractToolMaxCalls bounds how many calls one answer contributes. An
	// agent that searched eleven times says roughly what one that searched
	// three times says, and the later rounds are usually refinements of the
	// first.
	extractToolMaxCalls = 6
	// extractCitationMaxDocs bounds how many document titles one answer
	// contributes. An answer citing twelve chunks of the same handbook says
	// the same thing as one citing three.
	extractCitationMaxDocs = 4
	// extractMinRowRunes is the shortest truncation worth printing. Below it a
	// row is a fragment that costs budget and carries no meaning, so it is
	// dropped instead.
	extractMinRowRunes = 60
)

// evidenceOmitted marks where the budget cut rows out. It is printed rather
// than silently skipped so the model can tell "the user said nothing here"
// from "you were not shown it".
const evidenceOmitted = "[... omitted ...]"

// evidenceTier ranks a transcript row by how much it can be trusted to say
// something durable about the user. The declaration order is the order the
// budget is spent in.
type evidenceTier int

const (
	// tierUser is what the user typed: the only source of preferences,
	// constraints, corrections and dissatisfaction.
	tierUser evidenceTier = iota
	// tierAssistant is what was answered. Secondary evidence: it says what was
	// attempted and what the user then steered away from.
	tierAssistant
	// tierCitation is the documents an answer drew on, as titles only. Never
	// their content — a knowledge base is not a statement about its reader.
	tierCitation
	// tierTool is what the agent did to produce an answer: which tool, with
	// which arguments, and what came back.
	//
	// Last, and included at all for the same reason Codex includes it. The
	// record of what was tried is the part of a session that says why the
	// answer looks the way it does — three reformulated searches before one
	// returned anything is the shape of a question the knowledge base answers
	// badly, and no other row carries that. It ranks below the cited titles
	// because it is the most verbose and the least about the person.
	tierTool
)

var evidenceTiers = []evidenceTier{tierUser, tierAssistant, tierCitation, tierTool}

// label is how the tier is announced to the model. English, because the rest
// of the prompt structure is, and structure the model must not confuse with
// content has to read as structure.
func (t evidenceTier) label() string {
	switch t {
	case tierAssistant:
		return "assistant"
	case tierCitation:
		return "cited documents"
	case tierTool:
		return "tool"
	default:
		return "user"
	}
}

func (t evidenceTier) maxRunes() int {
	switch t {
	case tierAssistant:
		return extractAssistantRowMaxRunes
	case tierCitation:
		return extractCitationRowMaxRunes
	case tierTool:
		return extractToolRowMaxRunes
	default:
		return extractUserRowMaxRunes
	}
}

// transcriptLine is one row of evidence, kept with the identity of the message
// it came from so a produced memory can point back at it.
type transcriptLine struct {
	sessionID string
	messageID string
	at        time.Time
	tier      evidenceTier
	content   string
}

// transcriptSegment is one coherent stretch of conversation handed to the model
// as a unit: same session, no long silence in the middle.
type transcriptSegment struct {
	sessionID string
	// lines are every row the model may read, in the order they happened.
	lines []transcriptLine
	// end is the newest message timestamp the segment covers, including rows
	// that produced no evidence, and is what the watermark advances to.
	end   time.Time
	endID string
	// truncated records that the conversation was longer than one run reads,
	// so the account can say what it covers instead of implying it covers
	// everything.
	truncated bool
}

// start is when the conversation this segment covers began, as far as the
// segment can see. Zero for an empty segment.
func (s transcriptSegment) start() time.Time {
	if len(s.lines) == 0 {
		return time.Time{}
	}
	return s.lines[0].at
}

// userLineCount reports how many rows are the user's own words. A segment with
// none of them is not extractable: there is nothing in it that could say
// something about the person, and running the model on an assistant monologue
// is how a knowledge base's own text ends up stored as a user trait.
func (s transcriptSegment) userLineCount() int {
	count := 0
	for _, line := range s.lines {
		if line.tier == tierUser {
			count++
		}
	}
	return count
}

// render lays the segment out for the model, numbered so a decision can name
// the row it came from. The budget is the caller's because it depends on the
// window of the model about to read it.
func (s transcriptSegment) render(budget int) string {
	return renderEvidence(s.lines, budget, true)
}

// renderEvidence spends the budget tier by tier, newest row first, then prints
// what survived in chronological order.
//
// Numbering is by position in the full slice, not by position among the rows
// that survived, so a gap in the numbers is harmless and resolveSource can
// index straight back into lines.
func renderEvidence(lines []transcriptLine, budget int, numbered bool) string {
	if len(lines) == 0 {
		return ""
	}
	rendered := make([]string, len(lines))
	// Each selected row reserves room for one omission marker, plus one for a
	// gap after the last of them. Where the markers land is only known once
	// every tier has had its turn, and a budget that ignored them would be
	// overshot by exactly the amount of text that says something was left out.
	marker := len([]rune(evidenceOmitted)) + 1
	remaining := budget - marker
	for _, tier := range evidenceTiers {
		for index := len(lines) - 1; index >= 0; index-- {
			line := lines[index]
			if line.tier != tier {
				continue
			}
			text := formatEvidenceRow(line, index, numbered)
			cost := len([]rune(text)) + 1 + marker
			if cost > remaining {
				// Truncating the row keeps whatever the user actually said at
				// the end of a run, which is the part a follow-up run will
				// never see again. Below the floor there is nothing left worth
				// the space.
				if remaining-marker-1 < extractMinRowRunes {
					continue
				}
				text = truncateRunes(text, remaining-marker-2) + "…"
				cost = len([]rune(text)) + 1 + marker
			}
			remaining -= cost
			rendered[index] = text
		}
	}

	var builder strings.Builder
	gap := false
	for _, text := range rendered {
		if text == "" {
			if !gap {
				builder.WriteString(evidenceOmitted)
				builder.WriteString("\n")
				gap = true
			}
			continue
		}
		builder.WriteString(text)
		builder.WriteString("\n")
		gap = false
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatEvidenceRow(line transcriptLine, index int, numbered bool) string {
	if numbered {
		return fmt.Sprintf("[%d] (%s) [%s] %s",
			index+1, line.at.Format("2006-01-02 15:04"), line.tier.label(), line.content)
	}
	return fmt.Sprintf("[%s] %s", line.tier.label(), line.content)
}

// evidenceRows turns one stored message into the rows extraction may read.
//
// A system message produces nothing: it is the product talking to itself, and
// the instructions in it are exactly what must not be mistaken for something
// the user believes.
//
// Every row is redacted before it leaves this function. Redacting only the
// model's output, which is what used to happen, protects the store but not the
// user: a pasted token still travelled to the extraction model in full, and
// that call is the part of the pipeline that leaves the building. Codex redacts
// each row as it renders it, for the same reason.
func evidenceRows(message *types.Message) []transcriptLine {
	if message == nil {
		return nil
	}
	base := transcriptLine{
		sessionID: message.SessionID,
		messageID: message.ID,
		at:        message.CreatedAt,
	}
	content := strings.TrimSpace(message.Content)

	switch message.Role {
	case "user":
		// What the user pointed at is part of what they asked. An "@发票手册"
		// narrows a question the words alone leave open, and the caption of an
		// attached screenshot is often the whole question. Both are the user's
		// own act of supplying material, so they belong on the user row rather
		// than in a tier of their own.
		content = joinNonEmpty("\n", content,
			mentionedMaterial(message.MentionedItems),
			attachedMaterial(message.Images))
		if content == "" {
			return nil
		}
		row := base
		row.tier = tierUser
		row.content = redactRow(content, extractUserRowMaxRunes)
		return []transcriptLine{row}
	case "assistant":
		var rows []transcriptLine
		if content != "" {
			row := base
			row.tier = tierAssistant
			row.content = redactRow(content, extractAssistantRowMaxRunes)
			rows = append(rows, row)
		}
		if titles := citedTitles(message.KnowledgeReferences); titles != "" {
			row := base
			row.tier = tierCitation
			row.content = redactRow(titles, extractCitationRowMaxRunes)
			rows = append(rows, row)
		}
		for _, call := range toolCalls(message.AgentSteps) {
			row := base
			row.tier = tierTool
			row.content = redactRow(call, extractToolRowMaxRunes)
			rows = append(rows, row)
		}
		return rows
	default:
		return nil
	}
}

// redactRow bounds a row and strips credentials out of it.
//
// Truncate first, redact second. The other order lets a secret be cut in half
// by the ceiling and survive as an unmatched fragment.
func redactRow(content string, limit int) string {
	content = truncateRunes(content, limit)
	if redacted, changed := types.RedactSensitive(content); changed {
		return redacted
	}
	return content
}

// mentionedMaterial names the knowledge bases, files and skills the user
// attached to a question with "@".
func mentionedMaterial(items types.MentionedItems) string {
	if len(items) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if item.Type != "" {
			name = fmt.Sprintf("%s（%s）", name, item.Type)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	return "[attached] " + strings.Join(names, "；")
}

// attachedMaterial renders what an attached image turned out to say.
//
// Codex replaces an image with "[image omitted]" because it has nothing else;
// by the time a message is stored here the picture has already been read, and
// its caption is ordinary text. A bare placeholder would be strictly worse.
func attachedMaterial(images types.MessageImages) string {
	if len(images) == 0 {
		return ""
	}
	captions := make([]string, 0, len(images))
	for _, image := range images {
		if caption := strings.TrimSpace(image.Caption); caption != "" {
			captions = append(captions, caption)
		}
	}
	if len(captions) == 0 {
		return fmt.Sprintf("[image omitted ×%d]", len(images))
	}
	return "[image] " + strings.Join(captions, "；")
}

// toolCalls renders what the agent did, one row per call.
//
// One row per call rather than per step, so the budget can drop the eleventh
// search without losing the first. Reasoning text is deliberately absent: a
// ReAct scratchpad is the model talking to itself, and an account that quotes
// it ends up recording the model's guesses about the user as though the user
// had said them. Codex draws the same line — its lowest tier is tool calls and
// their output, while reasoning items are excluded from memory entirely.
func toolCalls(steps types.AgentSteps) []string {
	if len(steps) == 0 {
		return nil
	}
	rows := make([]string, 0, extractToolMaxCalls)
	for _, step := range steps {
		for i := range step.ToolCalls {
			call := step.ToolCalls[i]
			name := call.ExecutionName()
			if name == "" || isMemoryTool(name) {
				// A memory lookup is this system reading itself. Recording it
				// as evidence would let one account's existence become the
				// reason the next account mentions the same subject.
				continue
			}
			row := name
			if args := callArguments(call.Args); args != "" {
				row += "(" + args + ")"
			}
			switch {
			case call.Result == nil:
				row += " → (no result)"
			case !call.Result.Success:
				row += " → failed: " + strings.TrimSpace(call.Result.Error)
			case strings.TrimSpace(call.Result.Output) != "":
				row += " → " + strings.TrimSpace(call.Result.Output)
			default:
				row += " → (empty)"
			}
			rows = append(rows, row)
			if len(rows) >= extractToolMaxCalls {
				return rows
			}
		}
	}
	return rows
}

// isMemoryTool reports whether a tool call is memory reading itself.
func isMemoryTool(name string) bool {
	return strings.Contains(strings.ToLower(name), "memory")
}

// callArguments renders a tool's arguments compactly, keys sorted so the same
// call reads the same way across runs.
func callArguments(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprintf("%v", args[key]))
		if value == "" || value == "<nil>" {
			continue
		}
		// One argument cannot take the whole row: a tool handed a pasted
		// document would otherwise leave no room for what it returned.
		parts = append(parts, key+"="+truncateRunes(value, 120))
	}
	return strings.Join(parts, ", ")
}

// joinNonEmpty joins the parts that have something in them.
func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

// citedTitles lists the documents one answer drew on, de-duplicated.
//
// Titles only. The chunk text is knowledge-base content: it is what the product
// knows, not what this person is, and putting it in front of an extraction
// model is how a manual's sentences get stored as somebody's preferences.
func citedTitles(refs types.References) string {
	if len(refs) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(refs))
	titles := make([]string, 0, extractCitationMaxDocs)
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		title := strings.TrimSpace(ref.KnowledgeTitle)
		if title == "" {
			title = strings.TrimSpace(ref.KnowledgeFilename)
		}
		if title == "" {
			continue
		}
		if _, duplicate := seen[title]; duplicate {
			continue
		}
		seen[title] = struct{}{}
		titles = append(titles, title)
		if len(titles) >= extractCitationMaxDocs {
			break
		}
	}
	return strings.Join(titles, "；")
}

// collectSessionTranscript reads one conversation as a single unit.
//
// This is the shape the document layer needs, and it is a different shape from
// the one the old atomic store needed. Splitting a conversation into stretches
// separated by silence made sense when the output was a handful of short facts
// per stretch; it makes an account worse, because the thing being written is a
// history, and a history cut into arbitrary windows loses exactly what makes it
// one — what was tried first, what the user rejected, what it ended up as.
//
// So the whole conversation is read, up to a cap, and the account for it is
// rewritten from scratch each time the conversation grows. That re-reads turns
// an earlier run already saw, which is a real cost and the right one: it is
// what Codex does, and it is the only way the fifth turn's correction can
// change what the account says about the first turn's request.
//
// The watermark is still what decides whether a run happens at all. Returns
// hasNew=false when nothing the user said is past it, so a session that only
// grew an assistant retry does not pay for a rewrite.
//
// A conversation longer than the cap is read from its newest end, and the
// segment says so. Usually that loses nothing, because the turns off the front
// are already described in the previous account and the rewrite is shown that
// account and told to keep what still holds. The case it does lose something
// is a conversation that passed the cap before any account existed, and the
// point of the flag is that the model is told rather than being handed a
// beheaded transcript it will describe as though it were whole.
func (s *Service) collectSessionTranscript(
	ctx context.Context, session types.MemoryExtractionSession,
) (transcriptSegment, bool, error) {
	// One past the cap, so a conversation sitting exactly at it is not
	// reported as truncated.
	messages, err := s.messageRepo.GetRecentMessagesBySession(
		ctx, session.SessionID, episodeMaxMessagesPerRun+1)
	if err != nil {
		return transcriptSegment{}, false, fmt.Errorf("load session messages: %w", err)
	}

	segment := transcriptSegment{sessionID: session.SessionID}
	if len(messages) > episodeMaxMessagesPerRun {
		messages = messages[len(messages)-episodeMaxMessagesPerRun:]
		segment.truncated = true
	}
	hasNew := false
	for _, message := range messages {
		if message == nil {
			continue
		}
		rows := evidenceRows(message)
		segment.end, segment.endID = message.CreatedAt, message.ID
		segment.lines = append(segment.lines, rows...)
		for _, row := range rows {
			at := types.MemoryMessageCursor{At: message.CreatedAt, ID: message.ID}
			if row.tier == tierUser && at.After(session.Cursor) {
				hasNew = true
			}
		}
	}
	return segment, hasNew, nil
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}
