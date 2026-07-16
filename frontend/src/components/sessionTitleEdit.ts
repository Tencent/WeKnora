export const SESSION_TITLE_MAX_LENGTH = 80

export function normalizeSessionTitleDraft(value: string): string {
  return value.trim().replace(/\s+/g, ' ').slice(0, SESSION_TITLE_MAX_LENGTH)
}
