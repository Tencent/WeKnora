import { get } from '@/utils/request';

// ---------------------------------------------------------------------------
// Image Gallery API
//
// The gallery lists every image asset inside a knowledge base. An "asset" is a
// single rendered image projected from a chunk's `image_info` array (a document
// chunk can carry several images). The backend does keyword / attribute /
// enabled-state filtering and sorting in memory, then paginates — image sets
// per KB are small, and this keeps the code backend-agnostic (sqlite/postgres).
//
// What the gallery shows (filters, searchable fields, sort options) is NOT
// hardcoded here: GET /knowledge-bases/:id/gallery-config returns the
// self-describing contract (attribute sources, resolved per-attribute usage,
// the caller's search activation state) and the UI renders from it. New
// backend attributes therefore light up with no frontend change.
// ---------------------------------------------------------------------------

/** One image projected from a chunk, as returned by the backend. */
export interface ImageAsset {
  /** Stable id: "<chunkID>#<index-in-array>". */
  id: string;
  /** Owning chunk id. */
  chunk_id: string;
  /** Owning knowledge (document) id. */
  knowledge_id: string;
  /** Human-readable name of the source knowledge item (document title). */
  source_name: string;
  /** Owning chunk type; empty for images discovered on a text (document) chunk. */
  chunk_type: string;
  /** Rendered image URL. */
  url: string;
  /** Pre-transform source reference, when different. */
  original_url: string;
  /** Model-generated image description. */
  caption: string;
  /** Extracted OCR text, if any. */
  ocr_text: string;
  /** Observed attribute map keyed by source-local attribute name. */
  attrs: Record<string, unknown>;
  /** Mirrors the owning chunk's enabled flag. */
  is_enabled: boolean;
  /** Mirrors the owning chunk's index status. */
  status: number;
  /** Owning chunk creation time (ISO 8601). */
  created_at: string;
  /** Owning chunk last-update time (ISO 8601). */
  updated_at: string;
}

export type ImageSortOrder = 'asc' | 'desc';

/** Query parameters accepted by GET /knowledge-bases/:id/images. */
export interface ImageListParams {
  /** Case-insensitive substring match against the union of searchIn fields. */
  keyword?: string;
  /**
   * Namespaced attribute ids to search ("builtin:caption"). Must be
   * in_searchfield=true in the contract; the backend drops anything else.
   * Omit to use the backend default (builtin caption + ocr_text).
   */
  searchIn?: string[];
  /** Namespaced attribute id to sort by; must be in_sortfield=true. */
  sortBy?: string;
  sortOrder?: ImageSortOrder;
  /** Restrict to chunks with this enabled state. Omit to include both. */
  isEnabled?: boolean;
  /**
   * Attribute filters keyed by namespaced attribute id. Values within one
   * attribute are OR-ed; attributes are AND-ed. Sent as one `attr_filters`
   * JSON query param.
   */
  attrFilters?: Record<string, string[]>;
  page?: number;
  pageSize?: number;
}

export interface ImageListResult {
  items: ImageAsset[];
  total: number;
  page: number;
  pageSize: number;
}

/**
 * List image assets for a knowledge base. Attribute references use the
 * namespaced ids from the gallery contract; the backend validates them
 * against the contract and silently drops ineligible ones.
 */
export async function listGalleryImages(
  kbId: string,
  params: ImageListParams = {},
): Promise<ImageListResult> {
  const query = new URLSearchParams();
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.searchIn && params.searchIn.length) {
    query.set('search_in', params.searchIn.join(','));
  }
  if (params.sortBy) query.set('sort_by', params.sortBy);
  if (params.sortOrder) query.set('sort_order', params.sortOrder);
  if (typeof params.isEnabled === 'boolean') {
    query.set('is_enabled', String(params.isEnabled));
  }
  if (params.attrFilters && Object.keys(params.attrFilters).length) {
    query.set('attr_filters', JSON.stringify(params.attrFilters));
  }
  if (params.page) query.set('page', String(params.page));
  if (params.pageSize) query.set('page_size', String(params.pageSize));

  const qs = query.toString();
  const res = await get<{
    success: boolean;
    data: ImageAsset[];
    total: number;
    page: number;
    page_size: number;
  }>(`/api/v1/knowledge-bases/${kbId}/images${qs ? `?${qs}` : ''}`);

  return {
    items: res.data,
    total: res.total,
    page: res.page,
    pageSize: res.page_size,
  };
}

// ---------------------------------------------------------------------------
// Gallery contract (GET /knowledge-bases/:id/gallery-config)
// ---------------------------------------------------------------------------

/** Resolved per-attribute usage flags after all config tiers merged. */
export interface GalleryAttrUsage {
  in_filter: boolean;
  in_searchfield: boolean;
  in_sortfield: boolean;
}

/**
 * One attribute as the contract serves it: the source's declaration with
 * every config tier's overrides merged in. `id` is namespaced
 * ("<sourceID>:<name>"); `usage_from` names the highest tier that touched
 * the usage ("source" | "system" | "kb" | "user").
 */
/**
 * One allowed value of an attribute.
 *
 * Display text is deliberately split: `label` is the short name that fits a
 * filter checkbox, `description` is the sentence shown where there is room
 * (a tooltip). `value` stays the raw machine value — it is what gets
 * filtered and searched, and it is never localized.
 */
export interface GalleryAttrValue {
  value: string;
  label: string;
  description?: string;
}

export interface GalleryResolvedAttr {
  id: string;
  source: string;
  /** Source-local attribute name, as stored in image_info attrs. */
  name: string;
  /** "text" | "date" | "extent" | "presence" | "keywords" */
  type: string;
  values?: GalleryAttrValue[];
  /** Short on-screen name; falls back to the attribute id when absent. */
  label: string;
  /** Sentence explaining the attribute; shown where there is room. */
  description?: string;
  usage: GalleryAttrUsage;
  usage_from: string;
}

/** The self-describing contract the gallery UI renders from. */
export interface GalleryConfig {
  /** Live source ids in priority order. */
  attribute_sources: string[];
  attributes: GalleryResolvedAttr[];
  /** Search activation mode: "all" (default) or "custom". */
  mode: 'all' | 'custom';
  /** Per-attribute search toggles ("on"/"off"); consulted in custom mode. */
  status: Record<string, string>;
}

/** Fetch the gallery contract for a knowledge base. */
export async function fetchGalleryConfig(kbId: string): Promise<GalleryConfig> {
  const res = await get<{ success: boolean; data: GalleryConfig }>(
    `/api/v1/knowledge-bases/${kbId}/gallery-config`,
  );
  return {
    attribute_sources: res.data.attribute_sources ?? [],
    attributes: res.data.attributes ?? [],
    mode: res.data.mode === 'custom' ? 'custom' : 'all',
    status: res.data.status ?? {},
  };
}
