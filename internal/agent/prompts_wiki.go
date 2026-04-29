package agent

// Wiki ingest prompt templates for LLM-powered wiki page generation.
// These prompts are used by the wiki ingest pipeline to extract structured
// knowledge from raw documents and build/update wiki pages.

// WikiSummaryPrompt generates a summary page for a newly ingested document.
const WikiSummaryPrompt = `You are a wiki editor. Given the following document content, create a structured wiki summary page in Markdown format.

<document>
<title>{{.Title}}</title>
<file_name>{{.FileName}}</file_name>
<file_type>{{.FileType}}</file_type>
<content>
{{.Content}}
</content>
</document>

<available_wiki_pages>
{{.ExtractedSlugs}}
</available_wiki_pages>

<instructions>
1. The FIRST line of your output MUST be: SUMMARY: {one sentence, 15-40 words, describing what this document is about — for wiki index listing}
2. After the SUMMARY line, write a comprehensive summary of the document in Markdown format.
3. Include the key facts, arguments, and conclusions.
4. Use proper heading hierarchy (## for sections, ### for subsections).
5. **Wiki-link rule**: The available_wiki_pages list above maps slugs to display names and their aliases (format: "[[slug]] = display name (Aliases: a, b)"). Whenever you mention a name or alias that matches a listed entry, you MUST write it as [[slug|display name]] (e.g. [[entity/zhong-guo|中国]]), NOT as bold (**name**) or bare [[slug]]. Use the EXACT slugs provided — do NOT invent new slugs.
6. **Image rule**: If the document contains <images> tags with <image> elements, you SHOULD include the relevant images in your summary using the Markdown syntax: ![caption](url). Place the images where they are contextually relevant to the text.
7. At the end, include a "## Key Takeaways" section with bullet points.
8. Write in {{.Language}}.
9. Keep the summary concise but thorough (500-1500 words depending on document length).
</instructions>

Output the SUMMARY line first, then the Markdown content. Do not include any other preamble.`

// WikiKnowledgeExtractPrompt extracts both entities and concepts in a single LLM call.
// Returns a JSON object with "entities" and "concepts" arrays.
// This replaces the former separate WikiEntityExtractPrompt and WikiConceptExtractPrompt.
const WikiKnowledgeExtractPrompt = `You are a knowledge extraction system. Analyze the following document and extract all significant entities AND key concepts.

<document>
<title>{{.Title}}</title>
<content>
{{.Content}}
</content>
</document>

<previous_slugs>
{{.PreviousSlugs}}
</previous_slugs>

<instructions>
Return a JSON object with two arrays: "entities" and "concepts".
**IMPORTANT: Write ALL names, descriptions, and details in {{.Language}}**.

### Slug Continuity Rules
If previous slugs are provided above, you MUST follow these rules:
- If an entity or concept from the previous extraction still exists in the current document, **reuse its exact slug** from the previous list. Do NOT generate a new slug for the same thing.
- If an entity or concept no longer appears in the document, **do NOT include it** in the output.
- Only generate new slugs for entities/concepts that are genuinely new (not present in the previous list).
- This ensures slug stability across document updates.

### Entities (people, organizations, products, places, technologies, events, etc.)
Each entity should have:
- "name": The entity name in {{.Language}} (human-readable)
- "slug": URL-friendly slug, format "entity/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the entity was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME entity. Only include: official abbreviations (e.g. "IBM" for "International Business Machines"), full/short name variants (e.g. "腾讯" for "腾讯控股有限公司"), translations (e.g. "Apple" for "苹果公司"), and well-known alternate names (e.g. "Alphabet" for "Google母公司"). Do NOT include parent categories, related products, generic terms, or broader concepts. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in {{.Language}}. Describes WHAT this entity IS and its role in the document. Must be self-contained (understandable without reading the full page). This will be displayed in the wiki index.
- "details": A 2-5 sentence summary in {{.Language}} of key facts from the document. **Image rule**: If the document contains relevant <image> elements in an <images> tag, include them in the details using Markdown syntax: ![caption](url).

Only include entities that are substantively discussed (mentioned at least twice or described in detail). Do NOT include generic terms.

### Concepts (topics, themes, methodologies, theories, etc.)
Each concept should have:
- "name": The concept name in {{.Language}} (human-readable)
- "slug": URL-friendly slug, format "concept/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the concept was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME concept. Only include: official abbreviations (e.g. "RAG" for "Retrieval-Augmented Generation"), full/short name variants, and well-known synonyms used interchangeably in the field. Do NOT include sub-topics, related techniques, broader categories, or implementation details. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in {{.Language}}. Defines WHAT this concept IS. Must be self-contained (understandable without reading the full page). This will be displayed in the wiki index.
- "details": A 2-5 sentence explanation in {{.Language}} as discussed in the document. **Image rule**: If the document contains relevant <image> elements in an <images> tag, include them in the details using Markdown syntax: ![caption](url).

Only include concepts that are substantively discussed. Skip trivial or overly generic concepts.

### Deduplication Rules
- If something is a specific named thing (person, company, product, place), put it ONLY in "entities".
- If something is an abstract idea, methodology, or theory, put it ONLY in "concepts".
- Never duplicate items across the two arrays.

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If you need a newline in a string, you MUST use the escaped sequence \n.
</instructions>

Output ONLY valid JSON. Example:
{
  "entities": [
    {
      "name": "Acme Corp",
      "slug": "entity/acme-corp",
      "aliases": ["Acme", "Acme Corporation"],
      "description": "A technology company specializing in AI solutions.",
      "details": "Acme Corp was founded in 2020 and has grown to 500 employees. They focus on enterprise AI products and recently launched their flagship RAG platform."
    }
  ],
  "concepts": [
    {
      "name": "Retrieval-Augmented Generation",
      "slug": "concept/retrieval-augmented-generation",
      "aliases": ["RAG"],
      "description": "A technique that combines information retrieval with language model generation.",
      "details": "RAG works by first retrieving relevant documents from a knowledge base using vector similarity search, then feeding those documents as context to an LLM for answer generation."
    }
  ]
}`

// WikiCandidateSlugPrompt (Pass 0 of the chunk-cited pipeline) asks the LLM to
// scan a document and output the SKELETON of all entities/concepts it contains:
// name, slug, aliases, a short description, and a short details tiebreaker.
// The heavy lifting — linking each slug to concrete supporting chunks — is
// done in a second pass (see WikiChunkCitationPrompt). Because this prompt no
// longer has to carry full facts per item, it stays cheap even for long docs.
const WikiCandidateSlugPrompt = `You are a knowledge extraction system. Analyze the following document and list all significant entities AND key concepts as a lightweight candidate set. Another pass will later attach concrete supporting chunks to each item, so you do NOT need to write exhaustive per-item facts here.

<document>
<title>{{.Title}}</title>
<content>
{{.Content}}
</content>
</document>

<previous_slugs>
{{.PreviousSlugs}}
</previous_slugs>

<instructions>
Return a JSON object with two arrays: "entities" and "concepts".
**IMPORTANT: Write ALL names, descriptions, and details in {{.Language}}**.

### Extraction Scope (Granularity: {{.Granularity}})
{{.GranularityGuidance}}

### Slug Continuity Rules
If previous slugs are provided above, you MUST follow these rules:
- If an entity or concept from the previous extraction still exists in the current document, **reuse its exact slug** from the previous list. Do NOT generate a new slug for the same thing.
- If an entity or concept no longer appears in the document, **do NOT include it** in the output.
- Only generate new slugs for entities/concepts that are genuinely new (not present in the previous list).
- This ensures slug stability across document updates.

### Entities (people, organizations, products, places, technologies, events, etc.)
Each entity should have:
- "name": The entity name in {{.Language}} (human-readable).
- "slug": URL-friendly slug, format "entity/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the entity was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME entity. Only include: official abbreviations (e.g. "IBM" for "International Business Machines"), full/short name variants (e.g. "腾讯" for "腾讯控股有限公司"), translations, and well-known alternate names. Do NOT include parent categories, related products, generic terms, or broader concepts. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in {{.Language}}. Describes WHAT this entity IS and its role in the document. Must be self-contained. This will be displayed in the wiki index.
- "details": A short 1-3 sentence fallback summary in {{.Language}}. This is ONLY used when chunk-level citation fails downstream, so it does NOT need to be exhaustive. Keep it under 300 characters.

Apply the Extraction Scope rules above. Never promote trivially-mentioned names into entities.

### Concepts (topics, themes, methodologies, theories, etc.)
Each concept should have:
- "name": The concept name in {{.Language}} (human-readable).
- "slug": URL-friendly slug, format "concept/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the concept was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME concept. Only include: official abbreviations (e.g. "RAG" for "Retrieval-Augmented Generation"), full/short name variants, and well-known synonyms used interchangeably in the field. Do NOT include sub-topics, related techniques, broader categories, or implementation details. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in {{.Language}}. Defines WHAT this concept IS. Must be self-contained.
- "details": A short 1-3 sentence fallback summary in {{.Language}}. Keep it under 300 characters.

Apply the Extraction Scope rules above. Skip concepts that are merely name-dropped without discussion.

### Deduplication Rules
- If something is a specific named thing (person, company, product, place), put it ONLY in "entities".
- If something is an abstract idea, methodology, or theory, put it ONLY in "concepts".
- Never duplicate items across the two arrays.

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If you need a newline in a string, you MUST use the escaped sequence \n.
</instructions>

Output ONLY valid JSON. Example:
{
  "entities": [
    {
      "name": "Acme Corp",
      "slug": "entity/acme-corp",
      "aliases": ["Acme", "Acme Corporation"],
      "description": "A technology company specializing in AI solutions.",
      "details": "Founded in 2020, focuses on enterprise AI products."
    }
  ],
  "concepts": [
    {
      "name": "Retrieval-Augmented Generation",
      "slug": "concept/retrieval-augmented-generation",
      "aliases": ["RAG"],
      "description": "A technique that combines information retrieval with language model generation.",
      "details": "Retrieves documents, then feeds them as context to an LLM."
    }
  ]
}`

// WikiChunkCitationPrompt (Pass 1..N of the chunk-cited pipeline) asks the LLM
// to read a batch of chunks and, for each candidate entity/concept, list the
// chunk IDs that substantively discuss it. This keeps per-slug "facts" in
// their verbatim form (the chunk text) instead of asking the LLM to paraphrase.
const WikiChunkCitationPrompt = `You are a precise citation system. Your job is to scan a batch of document chunks and decide, for each candidate entity/concept below, which chunks substantively discuss it.

<document_title>{{.DocTitle}}</document_title>

<candidate_slugs>
{{.CandidateSlugs}}
</candidate_slugs>

<chunks>
{{.ChunksXML}}
</chunks>

<instructions>
**IMPORTANT: Write ALL names, descriptions, and details in {{.Language}}**.

### Primary task
For each candidate slug above, select the chunk IDs (from the <chunks> block) that **substantively discuss** that entity/concept. "Substantively" means the chunk states at least one concrete fact, attribute, step, date, number, relationship, or other useful piece of information about the candidate — not a passing mention.

- Only cite chunks that appear in the <chunks> block above.
- Use the "id" attribute of each <c> element verbatim (e.g. "c003").
- If a candidate is not meaningfully discussed in ANY chunk in this batch, omit it from the output (do not include empty arrays).
- A chunk CAN be cited by multiple candidates if it genuinely discusses multiple of them.
- If a chunk is overly long or mixes unrelated topics, still cite it for every candidate it discusses.

### Secondary task: new slugs
If this batch reveals a significant entity/concept that is **NOT** in <candidate_slugs>, you may add it under "new_slugs" so it gets incorporated. Only add genuinely new, substantively-discussed items. Do NOT rediscover items already listed above — reuse their slug if they are already candidates.

Each new slug must include:
- "type": "entity" or "concept"
- "name", "slug", "aliases", "description", "details" (same semantics as the candidate list)
- "source_chunks": list of chunk IDs in the current batch that discuss it

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If needed, use \n.
- Output ONLY valid JSON, no preamble.
</instructions>

Output format:
{
  "citations": {
    "entity/xxx": ["c001", "c003"],
    "concept/yyy": ["c002"]
  },
  "new_slugs": [
    {
      "type": "entity",
      "name": "Example",
      "slug": "entity/example",
      "aliases": [],
      "description": "...",
      "details": "...",
      "source_chunks": ["c005"]
    }
  ]
}

If nothing in this batch is cite-worthy, return: {"citations": {}, "new_slugs": []}`

// WikiPageModifyPrompt updates an existing wiki page with new additions and removes stale/deleted information in a single pass.
const WikiPageModifyPrompt = `You are a wiki editor tasked with updating an existing wiki page. You must process a set of NEW information to add, AND/OR a set of deleted documents whose exclusive contributions must be REMOVED.

<page_metadata>
  <slug>{{.PageSlug}}</slug>
  <title>{{.PageTitle}}</title>
  <type>{{.PageType}}</type>{{if .PageAliases}}
  <aliases>{{.PageAliases}}</aliases>{{end}}
</page_metadata>

This wiki page is specifically about **{{.PageTitle}}** (a {{.PageType}}). Every statement on the page MUST be directly about this exact {{.PageType}} — not about related, adjacent, or similarly-named things.

<existing_page_content>
{{.ExistingContent}}
</existing_page_content>

{{if .HasAdditions}}
<new_information>
{{.NewContent}}
</new_information>

The <new_information> block above is assembled from VERBATIM source chunks that were already cited as directly supporting this page. An optional <source_context> block inside each document is a document-level summary that tells you BOTH what the document is about AND what KIND of document it is (e.g. a resume, an announcement, a product page, a schedule) — use it to calibrate tone, stay on-topic, and avoid over-promotion. Do NOT quote the source_context text into the page; it is framing only.
{{end}}

{{if .HasRetractions}}
<deleted_documents>
{{.DeletedContent}}
</deleted_documents>

<remaining_source_documents>
{{.RemainingSourcesContent}}
</remaining_source_documents>
{{end}}

<valid_wiki_links>
{{.AvailableSlugs}}
</valid_wiki_links>

<instructions>
1. The FIRST line of your output MUST be: SUMMARY: {one sentence, 15-40 words, describing what this page is about after the update — for wiki index listing}
{{if .HasRetractions}}
2. REMOVE facts/claims that were ONLY sourced from the <deleted_documents> and are NOT present in any <remaining_source_documents> or <new_information>.
{{end}}
{{if .HasAdditions}}
3. ADD and MERGE the facts from <new_information> into the page. You are a COMPILER, not a writer:
   - **CRITICAL CONFLICT CHECK**: First verify that the <new_information> is actually about **{{.PageTitle}}** (as declared in <page_metadata>). If a piece of new info clearly belongs to a DIFFERENT but related thing (e.g., this page is about "Hunyuan Model" but the new info is about "Qwen3"; or this page is about "居民身份证" but the new info is about "工作居住证"), you MUST REJECT that part of the new information and DO NOT add it.
   - If it is genuinely about {{.PageTitle}} and contradicts old content, prefer the newer information.
   - **Stay close to source wording.** The chunks are verbatim. Reuse the source's own sentences; you MAY lightly reorder, deduplicate, and join related sentences, but do NOT rephrase for style, do NOT expand short statements into longer ones, and do NOT invent transitional sentences.
   - **Do NOT over-structure.** Only introduce a section heading (##, ###) if the source itself uses that heading OR the page already has one from existing content. For a new page with flat source text, a single "# {{.PageTitle}}" heading plus 1-2 short paragraphs and a flat bullet list of facts is PREFERRED over inventing a hierarchy of subsections.
   - **Do NOT add rhetorical filler.** Phrases like "旨在帮助…", "该平台致力于…", "具有重要意义", "designed to…", "aims to provide…" MUST NOT appear unless they are literally present in the source chunks.
   - **Scope discipline.** The source_context tells you whether the document is self-reported (e.g. a resume) or third-party authoritative. If the source is self-reported, do NOT elevate claims into industry-wide statements — stay descriptive and attribute when useful ("根据简历所述…" / "as described by…" is acceptable when the source is first-person).
{{end}}
4. Preserve existing information that is still valid and still about {{.PageTitle}}.
5. Keep [[slug|name]] wiki-link references ONLY if the slug appears in the <valid_wiki_links> list above. Remove any [[slug|name]] whose slug is NOT in that list. Do NOT invent new wiki-link slugs. The page's own slug ({{.PageSlug}}) MUST NOT appear as a [[...]] link inside its own content.
6. Maintain the existing page structure and formatting style. Use "# {{.PageTitle}}" as the top-level heading if the page does not already have one. Do NOT introduce new heading levels beyond what the source or existing page justifies.
7. **Image rule**: Include relevant images using Markdown syntax: ![caption](url) from new information if applicable.
{{if .HasRetractions}}
8. If after removing deleted content the page becomes nearly empty and there is no new information to add, output just: "SUMMARY: (empty page)\n# {{.PageTitle}}\n\n*This page's primary source document was removed.*"
{{end}}
9. Write in {{.Language}}.
</instructions>

Output the SUMMARY line first, then the updated Markdown content. Do not include any other preamble.`

// WikiIndexIntroPrompt generates the introduction for a NEW index page (first time only).
const WikiIndexIntroPrompt = `You are a wiki editor. Write a brief introduction for a wiki knowledge base index page.

<document_summaries>
{{.DocumentSummaries}}
</document_summaries>

<instructions>
1. Write a title line starting with "# " that reflects the knowledge domain.
2. Follow with 2-3 sentences describing what this wiki covers, based on the document summaries above.
3. Keep it concise — this is just the header section, the directory listing will be added separately below.
4. Write in {{.Language}}.
</instructions>

Output ONLY the title and introduction paragraph. Do NOT generate any directory listings or page links.`

// WikiIndexIntroUpdatePrompt incrementally updates an existing index introduction.
const WikiIndexIntroUpdatePrompt = `You are a wiki editor. Update the introduction section of a wiki index page to reflect recent changes.

<current_introduction>
{{.ExistingIntro}}
</current_introduction>

<changes>
{{.ChangeDescription}}
</changes>

<document_summaries>
{{.DocumentSummaries}}
</document_summaries>

<instructions>
1. Update the introduction to accurately reflect the current state of the wiki.
2. If documents were added, mention the new topics if they significantly change the wiki's scope.
3. If documents were removed, remove references to those topics if they no longer apply.
4. Keep the same tone, style, and title format as the existing introduction.
5. Keep it concise — 1 title line + 2-3 sentences.
6. Write in {{.Language}}.
</instructions>

Output ONLY the updated title and introduction paragraph. Do NOT generate any directory listings or page links.`

// WikiLogEntryTemplate is a simple template for log entries (not LLM-generated).
const WikiLogEntryTemplate = `## [{{.Date}}] {{.Operation}} | {{.Title}}
- **Source**: {{.SourceInfo}}
- **Pages affected**: {{.PagesAffected}}
- **Summary**: {{.Summary}}
`

// WikiDeduplicationPrompt asks the LLM to identify duplicate entities/concepts
// between newly extracted items and existing wiki pages.
const WikiDeduplicationPrompt = `You are a strict deduplication system. Given a list of newly extracted items and a list of existing wiki pages, determine which new items refer to the **exact same** real-world entity or concept as an existing page.

<new_items>
{{.NewItems}}
</new_items>

<existing_pages>
{{.ExistingPages}}
</existing_pages>

<instructions>
### Merge criteria — ALL must be true:
1. The new item and the existing page refer to the **same real-world thing** (same person, same organization, same specific concept).
2. The match is a **name variation**: abbreviation ↔ full name, translation, or minor spelling difference.
3. The types are compatible: entities merge with entities, concepts merge with concepts. **Never merge an entity into a concept or vice versa.**

### Examples of CORRECT merges:
- "Acme Corp" → "Acme Corporation" (same company, abbreviation)
- "RAG" → "Retrieval-Augmented Generation" (same concept, acronym)
- "苹果公司" → "Apple Inc." (same entity, translation)

### Examples of INCORRECT merges — do NOT merge these:
- "Hunyuan Model" → "Qwen Model" (competing products in the same category are DIFFERENT entities, do not merge them)
- "iPhone 15" → "Huawei Mate 60" (different specific instances in the same category)
- "GPT-4" → "GPT-3.5" (different versions of a product are distinct entities)
- "AI Safety" → "Content Review Mechanism" (related topics, but different concepts)
- "Athlete Registration" → "Degree Verification" (both involve verification, but completely different domains)
- "Competition Categories" → "Age Groups" (age groups are one aspect of categories, not the same concept)
- "Performance Standard" → "Competition Rounds" (both relate to competitions, but are different concepts)
- "Machine Learning" → "Neural Networks" (neural networks are a subset of ML, not the same concept)
- "居民身份证 / Resident ID Card" → "工作居住证 / Work Residence Permit" (both are government-issued documents but completely different credentials)
- "驾驶证 / Driver's License" → "行驶证 / Vehicle Registration" (both are car-related certificates but different documents)
- "学位证 / Degree Certificate" → "毕业证 / Graduation Certificate" (both educational documents but distinct)

### Key principle: **related ≠ same**. Two items sharing a few characters in their name, or belonging to the same domain / document family / industry, is NOT a reason to merge. **ABSOLUTELY DO NOT** merge different products, different companies, different versions, or different certificates/documents just because they belong to the same category. When in doubt, do NOT merge. It is far better to have two separate pages for the same thing than to wrongly merge two different things.

Return a JSON object with a "merges" map. The key is the NEW item's slug, the value is the EXISTING page's slug that it should merge into. Only include items where you are highly confident they are the same thing.

If no items match any existing pages, return: {"merges": {}}

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If you need a newline in a string, you MUST use the escaped sequence \n.
</instructions>

Output ONLY valid JSON. Example:
{"merges": {"entity/acme-corporation": "entity/acme-corp", "concept/rag": "concept/retrieval-augmented-generation"}}`

// Granularity guidance blocks injected into WikiCandidateSlugPrompt. The
// pipeline resolves a KnowledgeBase's configured granularity to one of these
// strings via WikiGranularityGuidance().
//
// The three levels form a spectrum from "only the document's main subjects"
// to "every named thing you see". Moving down the list monotonically
// increases the candidate slug count, the downstream chunk-citation cost,
// and the noise-to-signal ratio of the wiki index.
const (
	WikiGranularityGuidanceFocused = `**FOCUSED mode — aggressive pruning.**
Extract ONLY the document's primary subjects: the handful of entities/concepts that this document is fundamentally ABOUT.

INCLUDE:
- The document's main subject(s) — e.g. for a resume: the person and their named projects; for an announcement: the announcing organization and the event/product being announced; for a product page: the product itself and its maker.
- At most 3-7 items total across entities and concepts combined.

EXCLUDE (even if named explicitly):
- Technology stacks / libraries / frameworks mentioned in passing (e.g. a resume listing "Spring Boot, MySQL, Redis" — do NOT extract these).
- Generic concepts and methodologies that are merely referenced (e.g. "microservices", "async processing", "stateless authentication", "streaming response" mentioned as an implementation detail).
- Places, schools, or organizations mentioned only as background (e.g. alma mater of a resume owner, unless the document is ABOUT the school itself).
- Anything that would normally get a one-sentence description because there is not enough content to say more.

If you are unsure whether an item belongs, LEAVE IT OUT. A clean, focused index is more valuable than a comprehensive but noisy one.`

	WikiGranularityGuidanceStandard = `**STANDARD mode — balanced (default).**
Extract the document's main subjects PLUS entities/concepts that are substantively discussed — meaning they have a dedicated paragraph, multiple bullet points, or at least 2-3 sentences of context.

INCLUDE:
- The document's main subject(s).
- Secondary entities/concepts that receive a concrete block of content (a paragraph, a multi-point list, or a dedicated sub-section).
- Named methodologies, architectures, or techniques when the document explains HOW the subject uses them — not merely names them.

EXCLUDE:
- Items mentioned only in a comma-separated list of technologies without any further explanation (e.g. "Tech stack: A, B, C, D" — none of A/B/C/D are extracted unless they each also receive their own paragraph elsewhere).
- One-off mentions, parenthetical references, and generic infrastructure nouns.
- Items whose entire contribution to the document would fit in a single short sentence.

Aim for a tight, curated index. When in doubt about a marginal item, prefer to EXCLUDE it.`

	WikiGranularityGuidanceExhaustive = `**EXHAUSTIVE mode — maximum recall.**
Extract every named entity and every recognizable concept, including technologies, tools, standards, and methodologies mentioned even once by name, provided they are concrete and well-known (not generic terms like "database" or "function").

INCLUDE:
- All main and secondary subjects.
- All named technologies, libraries, frameworks, databases, services, protocols, or standards.
- All recognizable concepts and methodologies that have widely-used names (e.g. RAG, microservices, async processing, SSE, JWT).

EXCLUDE ONLY:
- Truly generic terms (e.g. "server", "function", "data").
- Items that appear only inside URL paths or reference citations.

Use this mode when the knowledge base functions as a technical glossary rather than a curated narrative wiki.`
)

// WikiGranularityGuidance returns the guidance text to inject into the
// WikiCandidateSlugPrompt template for the given granularity. Accepts the
// raw string value stored in WikiConfig.ExtractionGranularity; callers do
// NOT need to Normalize() first — unknown values fall through to standard.
func WikiGranularityGuidance(granularity string) string {
	switch granularity {
	case "focused":
		return WikiGranularityGuidanceFocused
	case "exhaustive":
		return WikiGranularityGuidanceExhaustive
	default:
		return WikiGranularityGuidanceStandard
	}
}
