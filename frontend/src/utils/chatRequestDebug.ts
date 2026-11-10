/** Sanitize stream POST body for display (strip large base64 payloads). */
export function sanitizeStreamRequestBody(body: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = { ...body };
  if (Array.isArray(out.images)) {
    out.images = out.images.map((img: { data?: string }, i: number) => ({
      _placeholder: `image[${i}]`,
      bytes: typeof img?.data === 'string' ? img.data.length : 0,
    }));
  }
  if (Array.isArray(out.attachment_uploads)) {
    out.attachment_uploads = out.attachment_uploads.map(
      (att: { file_name?: string; file_size?: number; data?: string }, i: number) => ({
        file_name: att.file_name,
        file_size: att.file_size,
        _placeholder: `attachment[${i}]`,
        bytes: typeof att?.data === 'string' ? att.data.length : 0,
      }),
    );
  }
  if (typeof out.query === 'string' && out.query.length > 500) {
    out.query = `${out.query.slice(0, 500)}… (${out.query.length} chars)`;
  }
  return out;
}

export interface StreamRequestMeta {
  requestId: string;
  url: string;
  method: string;
  body: Record<string, unknown> | null;
  sentAt: number;
}

export interface ChatRequestDebugInfo {
  requestId?: string;
  messageId?: string;
  sessionId?: string;
  url?: string;
  method?: string;
  body?: Record<string, unknown> | null;
  sentAt?: number;
}

export function buildChatRequestDebugPayload(info: ChatRequestDebugInfo): string {
  return JSON.stringify(info, null, 2);
}
