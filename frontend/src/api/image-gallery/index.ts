import { get } from '@/utils/request';

// ---------------------------------------------------------------------------
// Image Gallery API
//
// The gallery lists every image asset inside a knowledge base. An "asset" is a
// single rendered image projected from a chunk's `image_info` array (a document
// chunk can carry several images). The backend does keyword / attribute /
// enabled-state filtering and sorting in memory, then paginates — image sets
// per KB are small, and this keeps the code backend-agnostic (sqlite/postgres).
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
  /** Observed attribute map (e.g. contain.text, contain.data_visual). */
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

export type ImageSortBy = 'created_at' | 'updated_at' | 'caption';
export type ImageSortOrder = 'asc' | 'desc';

/** Query parameters accepted by GET /knowledge-bases/:id/images. */
export interface ImageListParams {
  /** Case-insensitive substring match against caption + ocr_text. */
  keyword?: string;
  sortBy?: ImageSortBy;
  sortOrder?: ImageSortOrder;
  /** Restrict to chunks with this enabled state. Omit to include both. */
  isEnabled?: boolean;
  /**
   * Attribute filters, keyed by attribute name (e.g. "contain.text").
   * Values within one attribute are OR-ed; attributes are AND-ed. An
   * attribute present here but unobserved on an image fails the match.
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
 * List image assets for a knowledge base.
 *
 * Attribute filters are sent as repeated `attr_<name>=<value>` query params
 * (one param per allowed value), matching the backend's repeated-param parsing
 * where values within an attribute are OR-ed.
 */
export async function listGalleryImages(
  kbId: string,
  params: ImageListParams = {},
): Promise<ImageListResult> {
  const query = new URLSearchParams();
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.sortBy) query.set('sort_by', params.sortBy);
  if (params.sortOrder) query.set('sort_order', params.sortOrder);
  if (typeof params.isEnabled === 'boolean') {
    query.set('is_enabled', String(params.isEnabled));
  }
  if (params.attrFilters) {
    for (const [name, values] of Object.entries(params.attrFilters)) {
      for (const v of values) {
        query.append(`attr_${name}`, v);
      }
    }
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
