# Relative Markdown document links in Wiki pages

Wiki pages use slug routes, so source-file links such as `[gateway](../gateway.md)` do not resolve in a Wiki page. Wiki creation and content updates now store the label (`gateway`) instead. This also applies to summaries. Inline formatting is preserved; code examples, images, external URLs, root-relative URLs, fragment links and `[[wiki-slug]]` links are unchanged.

Reference-style document links are normalized too. Unused relative document link definitions are removed; definitions shared with images are retained to preserve those images. This is the plain-label fallback, not a source-path-to-Wiki mapping feature.

## Repair existing pages

Use the existing Wiki maintenance APIs with the usual authentication and permissions:

1. `GET /api/v1/knowledgebase/:kb_id/wiki/lint` reports `relative_markdown_link` findings.
2. `POST /api/v1/knowledgebase/:kb_id/wiki/auto-fix` repairs these findings through the normal versioned page-update path. This endpoint also processes its other existing auto-fixable finding types; inspect the lint report first.

Repairs preserve the superseded page in revision history under the existing retention policy and reject conflicting concurrent edits. Repeating the repair does not create another revision for an already-normalized page. Source documents are not reparsed. Current Wiki content and the Wiki SQL search results use the updated stored text, so no separate vector reindex is necessary for Wiki pages.

The general `hybrid-search` endpoint retrieves source-document chunks, a separate path from Wiki page search. This change does not rewrite original source documents or their chunk indexes. Historical revision bodies also retain their original text. Source-path mapping and cleanup of source-document search results require separate handling.
