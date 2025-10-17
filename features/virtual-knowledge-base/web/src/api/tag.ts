import { apiClient } from "./http";

export interface TagCategory {
  id: number;
  name: string;
  description?: string;
  color?: string;
  created_at?: string;
  updated_at?: string;
}

export interface Tag {
  id: number;
  category_id: number;
  name: string;
  value: string;
  weight: number;
  description?: string;
  created_at?: string;
  updated_at?: string;
}

export interface DocumentTagAssignment {
  document_id: string;
  tag_id: number;
  weight?: number | null;
}

export const fetchTagCategories = async () => {
  const { data } = await apiClient.get<{ data: TagCategory[] }>("/categories");
  return data.data;
};

export const createTagCategory = async (payload: Partial<TagCategory>) => {
  const { data } = await apiClient.post<{ data: TagCategory }>("/categories", payload);
  return data.data;
};

export const updateTagCategory = async (id: number, payload: Partial<TagCategory>) => {
  const { data } = await apiClient.put<{ data: TagCategory }>(`/categories/${id}`, payload);
  return data.data;
};

export const deleteTagCategory = async (id: number) => {
  await apiClient.delete(`/categories/${id}`);
};

export const fetchTagsByCategory = async (categoryID: number) => {
  const { data } = await apiClient.get<{ data: Tag[] }>("/tags", { params: { category_id: categoryID } });
  return data.data;
};

export const createTag = async (payload: Partial<Tag>) => {
  const { data } = await apiClient.post<{ data: Tag }>("/tags", payload);
  return data.data;
};

export const updateTag = async (id: number, payload: Partial<Tag>) => {
  const { data } = await apiClient.put<{ data: Tag }>(`/tags/${id}`, payload);
  return data.data;
};

export const deleteTag = async (id: number) => {
  await apiClient.delete(`/tags/${id}`);
};
