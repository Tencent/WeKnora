import { apiClient } from "./http";

export type FilterOperator = "AND" | "OR" | "NOT";

export interface VirtualKBFilter {
  id?: number;
  virtual_kb_id?: number;
  tag_category_id: number;
  tag_ids: number[];
  operator: FilterOperator;
  weight: number;
}

export interface VirtualKB {
  id: number;
  name: string;
  description?: string;
  filters: VirtualKBFilter[];
  config?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface VirtualKBCreateRequest {
  name: string;
  description?: string;
  filters: VirtualKBFilter[];
  config?: Record<string, unknown>;
}

export interface VirtualKBUpdateRequest extends VirtualKBCreateRequest {
  id: number;
}

export const fetchVirtualKBs = async () => {
  const { data } = await apiClient.get<{ data: VirtualKB[] }>("/instances");
  return data.data;
};

export const fetchVirtualKB = async (id: number) => {
  const { data } = await apiClient.get<{ data: VirtualKB }>(`/instances/${id}`);
  return data.data;
};

export const createVirtualKB = async (payload: VirtualKBCreateRequest) => {
  const { data } = await apiClient.post<{ data: VirtualKB }>("/instances", payload);
  return data.data;
};

export const updateVirtualKB = async (id: number, payload: VirtualKBUpdateRequest) => {
  const { data } = await apiClient.put<{ data: VirtualKB }>(`/instances/${id}`, payload);
  return data.data;
};

export const deleteVirtualKB = async (id: number) => {
  await apiClient.delete(`/instances/${id}`);
};
