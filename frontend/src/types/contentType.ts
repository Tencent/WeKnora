export const knowledgeContentTypes = [
  'article',
  'book',
  'webpage',
  'meeting_notes',
  'report',
  'presentation',
  'spreadsheet',
  'manual',
  'other',
] as const;

export type KnowledgeContentType = typeof knowledgeContentTypes[number];

export interface ContentClassification {
  schema_version: number;
  type: KnowledgeContentType;
  source: 'ai' | 'rule' | 'manual';
  confidence?: number;
  matched_at?: string;
}

export function getContentClassification(metadata: unknown): ContentClassification | null {
  let value = metadata;
  if (typeof value === 'string') {
    try { value = JSON.parse(value); } catch { return null; }
  }
  const classification = (value as any)?.content_classification;
  return classification && knowledgeContentTypes.includes(classification.type)
    ? classification as ContentClassification
    : null;
}

export function setContentClassification(
  metadata: unknown,
  type: KnowledgeContentType,
): Record<string, unknown> {
  let value: Record<string, unknown> = {};
  if (typeof metadata === 'string') {
    try { value = JSON.parse(metadata) || {}; } catch { value = {}; }
  } else if (metadata && typeof metadata === 'object') {
    value = { ...(metadata as Record<string, unknown>) };
  }
  value.content_classification = {
    schema_version: 1,
    type,
    source: 'manual',
    confidence: 1,
    matched_at: new Date().toISOString(),
  };
  return value;
}

export function contentTypeOptions(t: (key: string) => string) {
  return knowledgeContentTypes.map((value) => ({
    value,
    label: t(`knowledgeBase.contentTypes.${value}`),
  }));
}
