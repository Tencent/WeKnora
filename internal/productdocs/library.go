// Package productdocs indexes the WeKnora documentation bundled with the
// server, so the built-in assistant can answer how the product works from
// the docs of the running version.
package productdocs

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	websitedocs "github.com/Tencent/WeKnora/website-docs"
)

// Categories are the documentation's sidebar groups, in site order. They
// mirror the sections list in website-docs/.vitepress/config.mts.
var Categories = []Category{
	{Dir: "01-getting-started", Label: "快速开始"},
	{Dir: "02-architecture", Label: "架构"},
	{Dir: "03-features", Label: "功能模块"},
	{Dir: "04-api", Label: "API 参考"},
	{Dir: "05-clients", Label: "客户端"},
	{Dir: "06-development", Label: "开发指南"},
	{Dir: "07-releases", Label: "版本发布"},
}

// Category is one sidebar group.
type Category struct {
	Dir   string
	Label string
}

// BM25 parameters; headings count several times so a section titled with
// the query outranks one that mentions it in passing.
const (
	bm25K1        = 1.2
	bm25B         = 0.75
	headingWeight = 3
	snippetRunes  = 160
)

// Library is a loaded, searchable set of pages.
type Library struct {
	docs     []*Doc
	byPath   map[string]*Doc
	sections []*indexedSection
	df       map[string]int
	avgLen   float64
}

type indexedSection struct {
	*Section
	tf     map[string]int
	length int
}

// Hit is one search result.
type Hit struct {
	Section *Section
	Score   float64
	Snippet string
}

var (
	defaultOnce sync.Once
	defaultLib  *Library
	defaultErr  error
)

// Default returns the library built from the pages embedded in the binary.
// As with Load, a non-nil library may come with an error naming the pages
// left out.
func Default() (*Library, error) {
	defaultOnce.Do(func() { defaultLib, defaultErr = Load(websitedocs.Pages) })
	return defaultLib, defaultErr
}

// Load parses every category's pages in fsys. A page that cannot be read or
// parsed is left out and reported in the error, alongside a library of the
// remaining pages, so one broken page does not take the others with it.
func Load(fsys fs.FS) (*Library, error) {
	lib := &Library{byPath: map[string]*Doc{}, df: map[string]int{}}
	var pageErrs []error
	for _, cat := range Categories {
		names, err := fs.Glob(fsys, cat.Dir+"/*.md")
		if err != nil {
			return nil, err
		}
		sort.Strings(names)
		for _, name := range names {
			raw, err := fs.ReadFile(fsys, name)
			if err != nil {
				pageErrs = append(pageErrs, err)
				continue
			}
			doc, err := parseDoc(name, cat.Label, string(raw))
			if err != nil {
				pageErrs = append(pageErrs, err)
				continue
			}
			lib.add(doc)
		}
	}
	if len(lib.docs) == 0 {
		return nil, errors.Join(append(pageErrs, fmt.Errorf("no documentation pages found"))...)
	}
	total := 0
	for _, s := range lib.sections {
		total += s.length
	}
	lib.avgLen = float64(total) / float64(len(lib.sections))
	return lib, errors.Join(pageErrs...)
}

func (l *Library) add(doc *Doc) {
	l.docs = append(l.docs, doc)
	l.byPath[doc.Path] = doc
	titleTokens := tokenize(doc.Title)
	for _, s := range doc.Sections {
		tf := map[string]int{}
		for _, tok := range tokenize(s.plain) {
			tf[tok]++
		}
		for _, tok := range tokenize(s.Heading + " " + strings.Join(s.Parents, " ")) {
			tf[tok] += headingWeight
		}
		for _, tok := range titleTokens {
			tf[tok]++
		}
		length := 0
		for tok, n := range tf {
			length += n
			l.df[tok]++
		}
		l.sections = append(l.sections, &indexedSection{Section: s, tf: tf, length: length})
	}
}

// Docs lists the pages of category (a Dir or Label), or all pages when empty.
func (l *Library) Docs(category string) []*Doc {
	var out []*Doc
	for _, d := range l.docs {
		if category == "" || inCategory(d, category) {
			out = append(out, d)
		}
	}
	return out
}

// Doc resolves a page by its path, file name or public URL.
func (l *Library) Doc(ref string) (*Doc, string, bool) {
	p, anchor := normalizeRef(ref)
	d, ok := l.byPath[p]
	return d, anchor, ok
}

// Search ranks sections for query, optionally within one category.
func (l *Library) Search(query, category string, limit int) []Hit {
	terms := uniqueTokens(tokenize(query))
	if len(terms) == 0 || limit <= 0 {
		return nil
	}
	n := float64(len(l.sections))
	var hits []Hit
	for _, s := range l.sections {
		if category != "" && !inCategory(s.Doc, category) {
			continue
		}
		score := 0.0
		for _, term := range terms {
			tf := float64(s.tf[term])
			if tf == 0 {
				continue
			}
			df := float64(l.df[term])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * tf * (bm25K1 + 1) /
				(tf + bm25K1*(1-bm25B+bm25B*float64(s.length)/l.avgLen))
		}
		if score > 0 {
			hits = append(hits, Hit{Section: s.Section, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	for i := range hits {
		hits[i].Snippet = snippet(hits[i].Section.plain, terms)
	}
	return hits
}

func inCategory(d *Doc, category string) bool {
	return strings.HasPrefix(d.Path, category+"/") || d.Category == category
}

// normalizeRef accepts "03-features/14-wiki", "03-features/14-wiki.md",
// "/docs/03-features/14-wiki" or the public URL, each with an optional
// "#anchor" that may be percent-encoded as a browser copies it.
func normalizeRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	ref, anchor, _ := strings.Cut(ref, "#")
	ref = strings.TrimPrefix(ref, PublicBaseURL)
	ref = strings.TrimPrefix(ref, "http://"+strings.TrimPrefix(PublicBaseURL, "https://"))
	ref = strings.TrimPrefix(ref, "/")
	ref = strings.TrimPrefix(ref, "docs/")
	ref = strings.TrimSuffix(strings.TrimSuffix(ref, "/"), ".md")
	ref = strings.TrimSuffix(ref, ".html")
	return path.Clean(ref), UnescapeAnchor(anchor)
}

// UnescapeAnchor decodes a percent-encoded anchor, leaving one that is not
// valid encoding as given.
func UnescapeAnchor(anchor string) string {
	if decoded, err := url.PathUnescape(anchor); err == nil {
		return decoded
	}
	return anchor
}

// tokenize lowercases text into words: runs of letters and digits, kept
// whole across "_", "." and "-" joins (OLLAMA_BASE_URL, v0.8.3, api-keys)
// and also split at them; Han text becomes overlapping character pairs,
// since Chinese has no spaces between words.
func tokenize(text string) []string {
	var out []string
	var word []rune
	var han []rune
	flushWord := func() {
		w := strings.TrimRight(string(word), "._-")
		word = word[:0]
		if utf8.RuneCountInString(w) < 2 {
			return
		}
		out = append(out, w)
		if strings.ContainsAny(w, "._-") {
			for _, part := range strings.FieldsFunc(w, isJoiner) {
				if utf8.RuneCountInString(part) > 1 {
					out = append(out, part)
				}
			}
		}
	}
	flushHan := func() {
		switch {
		case len(han) == 1:
			out = append(out, string(han))
		case len(han) > 1:
			for i := 0; i+1 < len(han); i++ {
				out = append(out, string(han[i:i+2]))
			}
		}
		han = han[:0]
	}
	runes := []rune(strings.ToLower(text))
	for i, r := range runes {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word = append(word, r)
		case isJoiner(r) && len(word) > 0 && i+1 < len(runes) &&
			(unicode.IsLetter(runes[i+1]) || unicode.IsDigit(runes[i+1])) && !unicode.Is(unicode.Han, runes[i+1]):
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return out
}

func isJoiner(r rune) bool { return r == '_' || r == '.' || r == '-' }

func uniqueTokens(tokens []string) []string {
	seen := map[string]bool{}
	out := tokens[:0]
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// snippet returns about snippetRunes of text around the first query term.
func snippet(text string, terms []string) string {
	text = strings.Join(strings.Fields(text), " ")
	lower := strings.ToLower(text)
	at := -1
	for _, term := range terms {
		if i := strings.Index(lower, term); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	runes := []rune(text)
	start := 0
	if at > 0 {
		start = utf8.RuneCountInString(lower[:at]) - snippetRunes/4
		if start < 0 {
			start = 0
		}
	}
	end := start + snippetRunes
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
