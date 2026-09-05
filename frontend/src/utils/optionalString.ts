export function normalizeOptionalString(value: string | null | undefined): string {
  return value ?? '';
}
