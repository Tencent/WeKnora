package compaction

// The sections are not decoration: the summary is the agent's only memory of
// the dropped turns, and unstructured prose reliably loses the two things the
// next round actually needs — what is already finished, and what was about to
// happen next.

const summarizationSystemPrompt = "" +
	"You are a context summarization assistant. Your task is to read a conversation " +
	"between a user and an AI assistant, then produce a structured summary following " +
	"the exact format specified.\n\n" +
	"Do NOT continue the conversation. Do NOT respond to any questions in the " +
	"conversation. ONLY output the structured summary."

const summaryFormat = "" +
	"## Goal\n" +
	"[What the user is ultimately trying to accomplish. May be multiple items.]\n\n" +
	"## Constraints & Preferences\n" +
	"- [Requirements, formats, tools, or styles the user asked for, and anything they rejected]\n" +
	"- [Or \"(none)\"]\n\n" +
	"## Progress\n" +
	"### Done\n" +
	"- [x] [Completed work, naming the specific artifacts produced and where they were written]\n\n" +
	"### In Progress\n" +
	"- [ ] [Current work]\n\n" +
	"### Blocked\n" +
	"- [Issues preventing progress, if any]\n\n" +
	"## Key Decisions\n" +
	"- **[Decision]**: [Brief rationale, so it is not revisited]\n\n" +
	"## Next Steps\n" +
	"1. [Ordered list of what should happen next]\n\n" +
	"## Critical Context\n" +
	"- [Facts, numbers, identifiers, paths, and error messages later steps depend on]\n" +
	"- [Or \"(none)\"]\n\n" +
	"Keep each section concise. Write in the same language as the conversation. " +
	"Preserve exact values — paths, names, numbers, and error text — rather than " +
	"paraphrasing them. Output only the summary, with no preamble.\n\n" +
	"Length matters: this summary shares the context window with the retained " +
	"recent messages, so it must stay compact enough to be worth having. Aim for " +
	"under 500 words. Prefer short bullets over prose, and drop detail that later " +
	"steps cannot act on."

const initialSummarizationInstructions = "" +
	"The messages above are a conversation to summarize. Create a structured context " +
	"checkpoint that another LLM will use to continue the work.\n\n" +
	"Use this EXACT format:\n\n" + summaryFormat

const updateSummarizationInstructions = "" +
	"The messages above are NEW conversation messages to incorporate into the existing " +
	"summary provided in <previous-summary> tags.\n\n" +
	"RULES:\n" +
	"- PRESERVE information from the previous summary that later steps still need\n" +
	"- ADD new progress, decisions, and context from the new messages\n" +
	"- UPDATE the Progress section: move items from \"In Progress\" to \"Done\" when completed\n" +
	"- UPDATE \"Next Steps\" based on what was accomplished\n" +
	"- PRESERVE exact file paths, function names, and error messages\n" +
	"- CONDENSE as you go: the result must not be longer than the previous " +
	"summary unless genuinely new facts require it. Completed work collapses to " +
	"one line each; superseded plans, resolved errors, and abandoned approaches " +
	"come out entirely. An update that only ever grows defeats the compaction " +
	"it is part of.\n\n" +
	"Use this EXACT format:\n\n" + summaryFormat

// turnPrefixInstructions summarizes the discarded head of a split turn. It is
// a different job from the history summary: the retained suffix is still in
// context, so this only has to supply what the suffix cannot explain about
// itself — above all, what the user originally asked for.
const turnPrefixInstructions = "" +
	"This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) " +
	"is retained in the conversation and is NOT shown here.\n\n" +
	"Summarize the prefix to provide context for the retained suffix, using this EXACT " +
	"format:\n\n" +
	"## Original Request\n" +
	"[What did the user ask for in this turn?]\n\n" +
	"## Early Progress\n" +
	"- [Key decisions and work done in the prefix, naming artifacts and paths]\n\n" +
	"## Context for Suffix\n" +
	"- [Information needed to understand the retained recent work]\n\n" +
	"Be concise. Write in the same language as the conversation. Preserve exact file " +
	"paths, names, and error messages. Output only the summary."

const splitTurnSeparator = "\n\n---\n\n**Turn Context (split turn):**\n\n"
