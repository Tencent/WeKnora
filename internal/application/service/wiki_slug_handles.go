package service

import "github.com/Tencent/WeKnora/internal/modelcontext"

// wikiSlugHandles maps real wiki slugs to short, low-entropy model handles
// (ref-1, ref-2, …) so the ingest editor LLM never has to reproduce a
// high-entropy slug verbatim — most importantly the UUID-based summary slugs
// (summary/<knowledgeID>) that models routinely mangle by inserting or
// dropping hex digits. The model copies the tiny handle; we translate handles
// back to real slugs on output.
//
// This is the generation-stage counterpart to wiki_write_page's slug
// validation: rather than repairing a mangled slug after the fact, it removes
// the opportunity to mangle at the source. It mirrors the chunk-handle
// (c000/c001) indirection already used by the chunk-citation pass in
// wiki_ingest_cite.go.
//
// Handle allocation and reverse lookup are owned by modelcontext.HandleTable;
// this wrapper contains only the Wiki-link rewrite semantics.
type wikiSlugHandles struct {
	table *modelcontext.HandleTable
}

func newWikiSlugHandles() *wikiSlugHandles {
	return &wikiSlugHandles{table: modelcontext.NewHandleTable("ref-", 0, 1)}
}

// handle returns the stable handle for a real slug, assigning a fresh one on
// first use. An empty slug returns "". Handles have no '/' so they can never
// collide with a namespaced real slug (entity/…, summary/…).
func (h *wikiSlugHandles) handle(realSlug string) string {
	if h == nil {
		return ""
	}
	return h.table.Register(realSlug)
}

// empty reports whether no handles have been assigned yet.
func (h *wikiSlugHandles) empty() bool { return h == nil || h.table.Empty() }

// encodeContent rewrites [[realSlug|disp]] / [[realSlug]] occurrences to their
// handle form, but only for slugs present in `known`. Unknown links are left
// untouched. Assigns handles on demand so a slug that appears only in the body
// (not in the listing) still gets a consistent handle.
func (h *wikiSlugHandles) encodeContent(content string, known map[string]struct{}) string {
	if content == "" || len(known) == 0 {
		return content
	}
	out, _ := rewriteDeadWikiLinks(content, func(norm, _ string) (string, bool) {
		if _, ok := known[norm]; !ok {
			return "", false
		}
		return h.handle(norm), true
	})
	return out
}

// decodeContent rewrites [[handle|disp]] / [[handle]] occurrences back to their
// real slug. Handles with no mapping are left untouched — they fall through to
// the ordinary parse/dead-link-cleanup path, which is the correct behavior for
// anything the model invented that isn't a known reference.
func (h *wikiSlugHandles) decodeContent(content string) string {
	if content == "" || h.empty() {
		return content
	}
	out, _ := rewriteDeadWikiLinks(content, func(norm, _ string) (string, bool) {
		real, ok := h.table.Resolve(norm)
		if !ok {
			return "", false
		}
		return real, true
	})
	return out
}
