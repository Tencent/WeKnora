package types

// WikiLeafKeywordCard is the minimal leaf record fed to the association
// LLM. The model sees identity, keywords, and a short description taken
// from the first markdown paragraph under the title. Content is only a
// prefix used to extract that paragraph; directory and source text are
// assembled afterwards by the program.
type WikiLeafKeywordCard struct {
	Slug     string      `json:"slug"`
	Title    string      `json:"title"`
	PageType string      `json:"page_type"`
	Aliases  StringArray `json:"aliases,omitempty"`
	Summary  string      `json:"summary,omitempty"`
	Content  string      `json:"content,omitempty"`
}

// WikiKnowledgeAssocResult is the query-time association payload: a
// directory tree that contains only the leaf knowledge points the model
// judged related to the question, with hierarchy and sources filled in
// by the program.
type WikiKnowledgeAssocResult struct {
	Query string                    `json:"query"`
	Tree  []*WikiKnowledgeAssocNode `json:"tree"`
	Pages []*WikiPage               `json:"pages,omitempty"`
}

// WikiKnowledgeAssocNode is one folder in the assembled association tree.
// Empty Name is the wiki root. Children and Leaves that would be empty
// after filtering are omitted.
type WikiKnowledgeAssocNode struct {
	Name     string                    `json:"name,omitempty"`
	Path     []string                  `json:"path,omitempty"`
	Children []*WikiKnowledgeAssocNode `json:"children,omitempty"`
	Leaves   []*WikiKnowledgeAssocLeaf `json:"leaves,omitempty"`
}

// WikiKnowledgeAssocLeaf is one related knowledge point with the
// surrounding association the program can reconstruct from stored rows.
type WikiKnowledgeAssocLeaf struct {
	Slug             string            `json:"slug"`
	Title            string            `json:"title"`
	PageType         string            `json:"page_type"`
	Aliases          StringArray       `json:"aliases,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	Content          string            `json:"content,omitempty"`
	ContentTruncated bool              `json:"content_truncated,omitempty"`
	CategoryPath     StringArray       `json:"category_path,omitempty"`
	WikiPath         string            `json:"wiki_path,omitempty"`
	FolderID         string            `json:"folder_id,omitempty"`
	Sources          []WikiAssocSource `json:"sources,omitempty"`
	Chunks           []WikiAssocChunk  `json:"chunks,omitempty"`
	InLinks          StringArray       `json:"in_links,omitempty"`
	OutLinks         StringArray       `json:"out_links,omitempty"`
}

// WikiAssocSource is a source document referenced by a wiki leaf.
type WikiAssocSource struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title,omitempty"`
}

// WikiAssocChunk is an original source chunk cited by a wiki leaf.
type WikiAssocChunk struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Content     string `json:"content,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// WikiSourceChunksReasonNoRefs is returned when a page has no chunk_refs
// (summary pages, or an ingest citation miss). The page still exists.
const WikiSourceChunksReasonNoRefs = "no_chunk_refs"

// WikiPageSourceChunksResult is the page-level evidence payload: every
// original chunk recorded on WikiPage.ChunkRefs, in stored order, with
// full text. Unlike AssociateLeaves this is not capped or truncated.
type WikiPageSourceChunksResult struct {
	KnowledgeBaseID string                `json:"knowledge_base_id"`
	Slug            string                `json:"slug"`
	Title           string                `json:"title"`
	PageType        string                `json:"page_type"`
	Sources         []WikiAssocSource     `json:"sources,omitempty"`
	Chunks          []WikiPageSourceChunk `json:"chunks"`
	ChunkRefCount   int                   `json:"chunk_ref_count"`
	MissingCount    int                   `json:"missing_count,omitempty"`
	Reason          string                `json:"reason,omitempty"`
}

// WikiPageSourceChunk is one cited original chunk for a wiki page.
type WikiPageSourceChunk struct {
	ID             string `json:"id"`
	KnowledgeID    string `json:"knowledge_id,omitempty"`
	KnowledgeTitle string `json:"knowledge_title,omitempty"`
	ChunkIndex     int    `json:"chunk_index,omitempty"`
	Content        string `json:"content,omitempty"`
	Missing        bool   `json:"missing,omitempty"`
}

// AsPage builds a WikiPage projection sufficient for scope / provenance
// checks from a source-chunks result.
func (r *WikiPageSourceChunksResult) AsPage() *WikiPage {
	if r == nil {
		return nil
	}
	refs := make(StringArray, 0, len(r.Sources))
	for _, src := range r.Sources {
		id := src.KnowledgeID
		if id == "" {
			continue
		}
		if src.Title != "" {
			refs = append(refs, id+"|"+src.Title)
		} else {
			refs = append(refs, id)
		}
	}
	return &WikiPage{
		KnowledgeBaseID: r.KnowledgeBaseID,
		Slug:            r.Slug,
		Title:           r.Title,
		PageType:        r.PageType,
		SourceRefs:      refs,
	}
}

// AsPage builds a WikiPage projection sufficient for scope / provenance
// checks without copying the full markdown body.
func (l *WikiKnowledgeAssocLeaf) AsPage() *WikiPage {
	if l == nil {
		return nil
	}
	refs := make(StringArray, 0, len(l.Sources))
	for _, src := range l.Sources {
		id := src.KnowledgeID
		if id == "" {
			continue
		}
		if src.Title != "" {
			refs = append(refs, id+"|"+src.Title)
		} else {
			refs = append(refs, id)
		}
	}
	return &WikiPage{
		Slug:         l.Slug,
		Title:        l.Title,
		PageType:     l.PageType,
		Aliases:      l.Aliases,
		Summary:      l.Summary,
		Content:      l.Content,
		CategoryPath: l.CategoryPath,
		WikiPath:     l.WikiPath,
		FolderID:     l.FolderID,
		SourceRefs:   refs,
		InLinks:      l.InLinks,
		OutLinks:     l.OutLinks,
	}
}
