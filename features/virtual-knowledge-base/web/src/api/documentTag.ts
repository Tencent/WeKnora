import { apiClient } from "./http";
import type { DocumentTagAssignment, Tag } from "./tag";

export const fetchDocumentTags = async (documentID: string) => {
  const { data } = await apiClient.get<{ data: Tag[] }>(`/documents/${documentID}/tags`);
  return data.data;
};

export const assignTagToDocument = async (documentID: string, payload: { tag_id: number; weight?: number | null }) => {
  const { data } = await apiClient.post<{ data: DocumentTagAssignment }>(`/documents/${documentID}/tags`, payload);
  return data.data;
};

export const updateDocumentTag = async (documentID: string, tagID: number, payload: { tag_id: number; weight?: number | null }) => {
  const { data } = await apiClient.put<{ data: DocumentTagAssignment }>(`/documents/${documentID}/tags/${tagID}`, payload);
  return data.data;
};

export const removeTagFromDocument = async (documentID: string, tagID: number) => {
  await apiClient.delete(`/documents/${documentID}/tags/${tagID}`);
};
