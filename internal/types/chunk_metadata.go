package types

import (
	"encoding/json"
)

// ChunkMetadata is the structured schema for the JSON blob stored in
// chunks.metadata. The chunk pipeline builds it as a loose map[string]any
// (see ParsedChunk.Metadata) using the Meta* key constants below to avoid
// accidental drift between producers and consumers.
//
// The shape mirrors the crawler-side `documents` table (see the ingest API
// docs) so rows flow end-to-end without reshaping. Fields split into three
// groups:
//
//  1. Structural — set by the chunker itself (heading_path, section_code,
//     category). One value per chunk, derived from text.
//  2. Provenance — copied from the crawler documents row onto every chunk
//     for cheap filter/weight/citation access. Denormalised intentionally
//     because chunk-level joins on every retrieval query would be painful.
//  3. Enrichment — filled asynchronously by the normalisation pipeline
//     (dictionary match + LLM NER). Empty until that pipeline runs.
//
// Forward-compatibility: add typed fields here as a schema stabilises;
// stash experimental fields in Extra so the JSON shape evolves without
// DB migrations.
type ChunkMetadata struct {
	// --- Group 1: Structural (set by the chunker) ---

	HeadingPath  []string `json:"heading_path,omitempty"`
	HeadingLevel int      `json:"heading_level,omitempty"`
	SectionCode  string   `json:"section_code,omitempty"`
	// Category tags the dominant content kind so retrieval and UI can filter
	// differently (e.g. show tables in a grid).
	// Values: "text" | "table" | "list" | "formula" | "code" | "mixed".
	Category string `json:"category,omitempty"`

	// --- Group 2: Provenance (copied from the crawler documents row) ---

	// SourceUUID is the crawler-side document UUID. Chunks from the same
	// document share this value; used for dedup, cross-system refs, and
	// reaching back to the crawler DB for fields we did not denormalise.
	SourceUUID string `json:"source_uuid,omitempty"`
	// ParentUUID is set for documents that hang off another doc (e.g. a PDF
	// attachment of an NMPA announcement). Enables drill-up retrieval.
	ParentUUID string `json:"parent_uuid,omitempty"`
	// DocType drives chunk strategy routing and user-facing filtering.
	// Values (non-exhaustive): "announcement" | "drug_insert" | "guideline"
	// | "paper" | "wechat_article" | "policy".
	DocType string `json:"doc_type,omitempty"`
	// SourceSite is the origin site identifier, e.g. "nmpa.gov.cn",
	// "fda.gov", "pubmed.ncbi.nlm.nih.gov", a WeChat account slug...
	SourceSite string `json:"source_site,omitempty"`
	// SourceCategory mirrors documents.category — a source-specific taxonomy
	// (e.g. NMPA uses "药品说明书修订" / "药品抽检公告" / ...).
	SourceCategory string `json:"source_category,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	SourceTitle    string `json:"source_title,omitempty"`
	Language       string `json:"language,omitempty"`
	// PublishedAt is the original publication date, ISO-8601 string.
	PublishedAt string `json:"published_at,omitempty"`
	PageStart   int    `json:"page_start,omitempty"`
	PageEnd     int    `json:"page_end,omitempty"`

	// AuthorityLevel is a WeKnora-side classification (L1..L4) derived from
	// SourceSite via a static mapping: L1 = 法规/药典/FDA; L2 = 指南/共识;
	// L3 = 同行评议论文; L4 = 公众号/综述. Orthogonal to ConfidenceLevel
	// (which is a crawler-side content-quality score).
	AuthorityLevel string `json:"authority_level,omitempty"`

	// --- Crawler quality assessment ---
	// ConfidenceLevel is the crawler's own A/B/C quality grade.
	ConfidenceLevel string `json:"confidence_level,omitempty"`
	// ConfidenceScore is the numeric form (0..1).
	ConfidenceScore float32 `json:"confidence_score,omitempty"`
	// ManualScore is the human-review quality score, populated after review.
	ManualScore float32 `json:"manual_score,omitempty"`

	// --- Crawler provenance details ---
	// ParseMethod tells us which parser produced the markdown we chunked
	// (e.g. "mineru", "html", "markdown_native"). Useful for debugging
	// and for routing to strategy-specific chunkers.
	ParseMethod string `json:"parse_method,omitempty"`
	// CrawlTimestamp is ISO-8601 of when the crawler fetched the document.
	CrawlTimestamp string `json:"crawl_timestamp,omitempty"`

	// --- Crawler-extracted doc-level entities (singular/scalar) ---
	// DrugName is the primary drug the document is about, if any. The
	// crawler surfaces one canonical name per document; chunk-level
	// multi-drug detection lives in Drugs (below, filled by enrichment).
	DrugName string `json:"drug_name,omitempty"`
	// ApprovalNumber is e.g. "国药准字H20xxxxx" or FDA NDC.
	ApprovalNumber string `json:"approval_number,omitempty"`
	Manufacturer   string `json:"manufacturer,omitempty"`
	// Keywords are the crawler-assigned tags. Treat as BM25 boost signals.
	Keywords []string `json:"keywords,omitempty"`

	// --- Group 3: Enrichment (filled asynchronously post-ingest) ---

	// Drugs are per-chunk normalised drug names (INN / 通用名) detected
	// inside the chunk's text by the dictionary + LLM pipeline.
	Drugs []string `json:"drugs,omitempty"`
	// Diseases are per-chunk normalised disease names.
	Diseases []string `json:"diseases,omitempty"`
	// ICD10 codes for diseases mentioned in the chunk.
	ICD10 []string `json:"icd10,omitempty"`
	// MeSHTerms are MeSH subject headings (useful for biomedical literature).
	MeSHTerms []string `json:"mesh_terms,omitempty"`

	// Extra is the escape hatch for fields that are not yet first-class
	// (e.g. source-site-specific keys). New ingest API clients may drop
	// structured payloads here without requiring a code change.
	Extra map[string]any `json:"extra,omitempty"`
}

// Canonical metadata keys. Use these everywhere instead of string literals
// to prevent drift between the chunker, service layer, and retrieval layer.
const (
	// Group 1: structural
	MetaKeyHeadingPath  = "heading_path"
	MetaKeyHeadingLevel = "heading_level"
	MetaKeySectionCode  = "section_code"
	MetaKeyCategory     = "category"

	// Group 2: provenance
	MetaKeySourceUUID      = "source_uuid"
	MetaKeyParentUUID      = "parent_uuid"
	MetaKeyDocType         = "doc_type"
	MetaKeySourceSite      = "source_site"
	MetaKeySourceCategory  = "source_category"
	MetaKeySourceURL       = "source_url"
	MetaKeySourceTitle     = "source_title"
	MetaKeyLanguage        = "language"
	MetaKeyPublishedAt     = "published_at"
	MetaKeyPageStart       = "page_start"
	MetaKeyPageEnd         = "page_end"
	MetaKeyAuthorityLevel  = "authority_level"
	MetaKeyConfidenceLevel = "confidence_level"
	MetaKeyConfidenceScore = "confidence_score"
	MetaKeyManualScore     = "manual_score"
	MetaKeyParseMethod     = "parse_method"
	MetaKeyCrawlTimestamp  = "crawl_timestamp"
	MetaKeyDrugName        = "drug_name"
	MetaKeyApprovalNumber  = "approval_number"
	MetaKeyManufacturer    = "manufacturer"
	MetaKeyKeywords        = "keywords"

	// Group 3: enrichment
	MetaKeyDrugs     = "drugs"
	MetaKeyDiseases  = "diseases"
	MetaKeyICD10     = "icd10"
	MetaKeyMeSHTerms = "mesh_terms"

	// Escape hatch
	MetaKeyExtra = "extra"
)

// Chunk category values for MetaKeyCategory.
const (
	ChunkCategoryText    = "text"
	ChunkCategoryTable   = "table"
	ChunkCategoryList    = "list"
	ChunkCategoryFormula = "formula"
	ChunkCategoryCode    = "code"
	ChunkCategoryMixed   = "mixed"
)

// Document type values for MetaKeyDocType. These drive chunker-strategy
// routing inside the ingest API: announcement/guideline → markdown,
// drug_insert → drug_insert_splitter, paper → academic_splitter, etc.
const (
	DocTypeAnnouncement  = "announcement"   // NMPA / FDA 公告
	DocTypeDrugInsert    = "drug_insert"    // 药品说明书
	DocTypeGuideline     = "guideline"      // 临床指南 / 专家共识
	DocTypePaper         = "paper"          // 同行评议论文
	DocTypePolicy        = "policy"         // 卫健委政策、医保政策
	DocTypeWeChatArticle = "wechat_article" // 公众号推文
	DocTypeReview        = "review"         // 综述 / 博客
)

// Authority levels for MetaKeyAuthorityLevel. Derived from SourceSite,
// NOT from crawler input, so the mapping can evolve centrally without
// re-crawling. See internal/application/service/authority_mapping.go
// (TODO: to be introduced with the ingest API).
const (
	AuthorityL1 = "L1" // 法规 / 药典 / NMPA / FDA / EMA
	AuthorityL2 = "L2" // 临床指南 / 专家共识 / WHO
	AuthorityL3 = "L3" // 同行评议论文（PubMed / 核心期刊）
	AuthorityL4 = "L4" // 公众号 / 综述博客 / 非官方翻译
)

// MarshalChunkMetadata converts a loose metadata map produced by the
// chunker / enrichment pipeline into a JSON value ready for the
// chunks.metadata column. Returns nil when the input is empty so the DB
// sees NULL instead of "{}".
func MarshalChunkMetadata(m map[string]any) (JSON, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return JSON(b), nil
}

// MergeChunkMetadata merges enrichment overrides into a base map without
// mutating the inputs. Values in override win over base. Nil-safe.
// Useful when the ingest API hands a provenance map down and the chunker
// adds structural keys on top.
func MergeChunkMetadata(base, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
