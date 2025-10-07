import { apiClient } from "./http";

export interface SearchTagFilter {
  tag_category_id: number;
  tag_ids: number[];
  operator: "AND" | "OR" | "NOT";
  weight: number;
}

export interface EnhancedSearchRequest {
  virtual_kb_id?: number;
  tag_filters?: SearchTagFilter[];
  limit?: number;
}

export interface DocumentScore {
  document_id: string;
  score: number;
}

export interface EnhancedSearchResponse {
  results: DocumentScore[];
}

export const enhancedSearch = async (payload: EnhancedSearchRequest) => {
  const { data } = await apiClient.post<{ data: EnhancedSearchResponse }>("/search", payload);
  return data.data;
};
