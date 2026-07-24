package service

import (
	"fmt"
	"strings"
)

// slugAliaser maps real wiki slugs to short, low-entropy reference tokens
// (ref-1, ref-2, …) so the ingest editor LLM never has to reproduce a
// high-entropy slug verbatim — most importantly the UUID-based summary slugs
// (summary/<knowledgeID>) that models routinely mangle by inserting or
// dropping hex digits. The model copies the tiny token; we translate tokens
// back to real slugs on output.
//
// This is the generation-stage counterpart to wiki_write_page's slug
// validation: rather than repairing a mangled slug after the fact, it removes
// the opportunity to mangle at the source. It mirrors the chunk-alias
// (c000/c001) indirection already used by the chunk-citation pass in
// wiki_ingest_cite.go.
//
// A slugAliaser is single-use and NOT safe for concurrent use; construct one
// per LLM call.
type slugAliaser struct {
	toToken map[string]string // real slug -> token
	toSlug  map[string]string // token -> real slug
}

func newSlugAliaser() *slugAliaser {
	return &slugAliaser{
		toToken: make(map[string]string),
		toSlug:  make(map[string]string),
	}
}

// token returns the stable token for a real slug, assigning a fresh one on
// first use. An empty slug returns "". Tokens have no '/' so they can never
// collide with a namespaced real slug (entity/…, summary/…).
func (a *slugAliaser) token(realSlug string) string {
	realSlug = strings.TrimSpace(realSlug)
	if realSlug == "" {
		return ""
	}
	if t, ok := a.toToken[realSlug]; ok {
		return t
	}
	t := fmt.Sprintf("ref-%d", len(a.toToken)+1)
	a.toToken[realSlug] = t
	a.toSlug[t] = realSlug
	return t
}

// empty reports whether no tokens have been assigned yet (nothing to alias).
func (a *slugAliaser) empty() bool { return len(a.toToken) == 0 }

// aliasContent rewrites [[realSlug|disp]] / [[realSlug]] occurrences to their
// token form, but only for slugs present in `known`. Unknown links are left
// untouched. Assigns tokens on demand so a slug that appears only in the body
// (not in the listing) still gets a consistent token.
func (a *slugAliaser) aliasContent(content string, known map[string]struct{}) string {
	if content == "" || len(known) == 0 {
		return content
	}
	out, _ := rewriteDeadWikiLinks(content, func(norm, _ string) (string, bool) {
		if _, ok := known[norm]; !ok {
			return "", false
		}
		return a.token(norm), true
	})
	return out
}

// dealiasContent rewrites [[token|disp]] / [[token]] occurrences back to their
// real slug. Tokens with no mapping are left untouched — they fall through to
// the ordinary parse/dead-link-cleanup path, which is the correct behavior for
// anything the model invented that isn't a known reference.
func (a *slugAliaser) dealiasContent(content string) string {
	if content == "" || a.empty() {
		return content
	}
	out, _ := rewriteDeadWikiLinks(content, func(norm, _ string) (string, bool) {
		real, ok := a.toSlug[norm]
		if !ok {
			return "", false
		}
		return real, true
	})
	return out
}
