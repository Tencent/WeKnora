// models.display_name is VARCHAR(255). Keep this aligned with
// types.ModelDisplayNameMaxLen.
export const MODEL_DISPLAY_NAME_MAX_LEN = 255

const MAX_COPY_ATTEMPTS = 10000

export function modelCopyLabel(model: { display_name?: string | null; name?: string | null }): string {
  const displayName = model.display_name?.trim()
  return displayName || model.name || ''
}

// generateCopyDisplayName builds a display label that is not already used
// and fits in the display_name column. The model name is not part of the
// result; callers keep the source name unchanged.
export function generateCopyDisplayName(
  originalDisplayName: string,
  existingLabels: Iterable<string>,
  suffix: string,
): string {
  const base = originalDisplayName.trim()
  const taken = new Set(existingLabels)
  let counter: number | undefined
  let candidate = fitCopyDisplayName(base, suffix, counter).trim()
  for (let attempt = 0; attempt < MAX_COPY_ATTEMPTS; attempt++) {
    if (candidate && !taken.has(candidate)) {
      return candidate
    }
    counter = counter == null ? 2 : counter + 1
    const next = fitCopyDisplayName(base, suffix, counter).trim()
    if (next === candidate) {
      break
    }
    candidate = next
  }
  throw new Error('unique copy display name exceeds the length limit')
}

function fitCopyDisplayName(base: string, suffix: string, counter?: number): string {
  const tail = counter == null ? suffix : `${suffix} ${counter}`
  const tailChars = Array.from(tail)
  const baseChars = Array.from(base)
  if (tailChars.length >= MODEL_DISPLAY_NAME_MAX_LEN) {
    return tailChars.slice(tailChars.length - MODEL_DISPLAY_NAME_MAX_LEN).join('')
  }
  const room = MODEL_DISPLAY_NAME_MAX_LEN - tailChars.length
  return baseChars.slice(0, room).join('') + tail
}
