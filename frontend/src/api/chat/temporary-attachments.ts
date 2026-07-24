import { del, get, getDown, postUpload } from '@/utils/request';

export type TemporaryAttachmentStatus = 'uploaded' | 'processing' | 'ready' | 'failed';

export interface TemporaryAttachment {
  id: string;
  session_id: string;
  file_name: string;
  file_type: string;
  file_size: number;
  mime_type?: string;
  status: TemporaryAttachmentStatus;
  token_count: number;
  chunk_count: number;
  image_refs?: Array<{ original_ref?: string; url: string; mime_type?: string }>;
  error_message?: string;
  expires_at: string;
}

interface AttachmentResponse {
  success: boolean;
  data: TemporaryAttachment;
}

export function uploadTemporaryAttachment(
  sessionId: string,
  file: File,
  agentId?: string,
  parserEngine?: string,
  onProgress?: (percent: number) => void,
): Promise<AttachmentResponse> {
  const form = new FormData();
  form.append('file', file);
  if (agentId) form.append('agent_id', agentId);
  if (parserEngine) form.append('parser_engine', parserEngine);
  return postUpload(
    `/api/v1/sessions/${sessionId}/attachments`,
    form,
    (event) => {
      if (event.total) onProgress?.(Math.round((event.loaded * 100) / event.total));
    },
  );
}

export function getTemporaryAttachment(sessionId: string, attachmentId: string): Promise<AttachmentResponse> {
  return get(`/api/v1/sessions/${sessionId}/attachments/${attachmentId}`);
}

export function previewTemporaryAttachment(sessionId: string, attachmentId: string): Promise<Blob> {
  return getDown(`/api/v1/sessions/${sessionId}/attachments/${attachmentId}/preview`);
}

export function deleteTemporaryAttachment(sessionId: string, attachmentId: string): Promise<void> {
  return del(`/api/v1/sessions/${sessionId}/attachments/${attachmentId}`);
}
