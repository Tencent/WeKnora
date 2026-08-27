export const QUESTION_TICK_RADIUS_PX = 3
export const QUESTION_TICK_GAP_PX = 35
export const ACTIVE_QUESTION_TOP_OFFSET_PX = 72
export const VIEWPORT_BAND_MIN_HEIGHT_PX = 16
export const QUESTION_MINIMAP_TRACK_MAX_PX = 360
export const QUESTION_MINIMAP_TRACK_RATIO = 0.5

export function questionMinimapTrackHeight(
  questionCount: number,
  clientHeight: number,
): number {
  if (questionCount <= 0 || clientHeight <= 0) return 0
  const maxHeight = Math.min(
    QUESTION_MINIMAP_TRACK_MAX_PX,
    clientHeight * QUESTION_MINIMAP_TRACK_RATIO,
  )
  const naturalHeight = questionCount === 1
    ? QUESTION_TICK_GAP_PX
    : (questionCount - 1) * QUESTION_TICK_GAP_PX
  return Math.min(naturalHeight, maxHeight)
}

export type ChatMessageLike = {
  id?: string
  role?: string
  content?: string
  images?: unknown[]
  attachments?: unknown[]
}

export type UserQuestion = {
  id: string
  content: string
  hasAttachments: boolean
}

export type QuestionTick = {
  id: string
  yRatio: number
  yPx: number
}

export type ViewportBand = {
  topPx: number
  heightPx: number
}

export function isChatOverflowing(scrollHeight: number, clientHeight: number): boolean {
  return scrollHeight > clientHeight
}

export function shouldShowQuestionMinimap(overflowing: boolean, questionCount: number): boolean {
  return overflowing && questionCount >= 1
}

export function collectUserQuestions(messages: ChatMessageLike[]): UserQuestion[] {
  return messages
    .filter((message) => message.role === 'user' && message.id)
    .map((message) => ({
      id: message.id!,
      content: message.content ?? '',
      hasAttachments:
        (message.images?.length ?? 0) > 0 || (message.attachments?.length ?? 0) > 0,
    }))
}

export function questionDisplayText(
  content: string | undefined,
  attachmentPlaceholder: string,
): string {
  const normalized = (content ?? '').replace(/\s+/g, ' ').trim()
  return normalized.length > 0 ? normalized : attachmentPlaceholder
}

export function offsetFromScrollContent(
  anchorTop: number,
  containerTop: number,
  scrollTop: number,
): number {
  return anchorTop - containerTop + scrollTop
}

export function mapQuestionTicks(
  items: Array<{ id: string }>,
  trackHeight: number,
): QuestionTick[] {
  const n = items.length
  if (n === 0 || trackHeight <= 0) {
    return []
  }

  if (n === 1) {
    return [{
      id: items[0].id,
      yRatio: 0.5,
      yPx: trackHeight / 2,
    }]
  }

  const preferredSpan = (n - 1) * QUESTION_TICK_GAP_PX
  const minY = Math.min(QUESTION_TICK_RADIUS_PX, trackHeight / 2)
  const maxY = Math.max(minY, trackHeight - minY)
  const available = maxY - minY
  const span = preferredSpan <= trackHeight ? preferredSpan : available
  const start = (trackHeight - span) / 2

  return items.map((item, index) => {
    const yRatio = index / (n - 1)
    return {
      id: item.id,
      yRatio,
      yPx: start + yRatio * span,
    }
  })
}

export function viewportBand(
  scrollTop: number,
  clientHeight: number,
  scrollHeight: number,
  trackHeight: number,
  minHeightPx: number = VIEWPORT_BAND_MIN_HEIGHT_PX,
): ViewportBand {
  if (scrollHeight <= 0 || trackHeight <= 0) {
    return { topPx: 0, heightPx: minHeightPx }
  }

  let topPx = (scrollTop / scrollHeight) * trackHeight
  let heightPx = Math.max(minHeightPx, (clientHeight / scrollHeight) * trackHeight)

  heightPx = Math.min(heightPx, trackHeight)

  if (topPx + heightPx > trackHeight) {
    topPx = trackHeight - heightPx
  }

  topPx = Math.max(0, topPx)

  if (topPx + heightPx > trackHeight) {
    heightPx = trackHeight - topPx
  }

  return { topPx, heightPx }
}

export function activeQuestionId(
  items: Array<{ id: string; offsetTop: number }>,
  scrollTop: number,
  topOffsetPx: number = ACTIVE_QUESTION_TOP_OFFSET_PX,
): string | null {
  if (items.length === 0) {
    return null
  }

  const threshold = scrollTop + topOffsetPx
  let activeId: string | null = null

  for (const item of items) {
    if (item.offsetTop <= threshold) {
      activeId = item.id
    }
  }

  return activeId ?? items[0].id
}
