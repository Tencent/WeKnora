export const QUESTION_TICK_INSET_PX = 8
export const QUESTION_TICK_GAP_PX = 8
export const QUESTION_TICK_MOUNTAIN_GAIN = 0.8
export const CURRENT_TICK_SCALE = 1.55
export const ACTIVE_QUESTION_TOP_OFFSET_PX = 72
export const VIEWPORT_BAND_MIN_HEIGHT_PX = 16
export const QUESTION_MINIMAP_TRACK_MAX_PX = 360
export const QUESTION_MINIMAP_TRACK_RATIO = 0.5
// 28px rail + 4px hit-area extension + 12px clearance from message content.
export const QUESTION_MINIMAP_MIN_GUTTER_PX = 44

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
    ? QUESTION_TICK_INSET_PX * 2
    : (questionCount - 1) * QUESTION_TICK_GAP_PX + QUESTION_TICK_INSET_PX * 2
  return Math.min(naturalHeight, maxHeight)
}

export type ChatMessageLike = {
  id?: string
  role?: string
  content?: string
  images?: unknown[]
  attachments?: unknown[]
}

export type OutlineMessage = {
  id: string
  role: 'user' | 'assistant'
  content: string
  hasAttachments: boolean
  answerContent: string
  messageIds: string[]
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

export function shouldShowQuestionMinimap(overflowing: boolean, questionCount: number, availableGutter: number): boolean {
  return overflowing && questionCount >= 1 && availableGutter >= QUESTION_MINIMAP_MIN_GUTTER_PX
}

export function collectOutlineMessages(messages: ChatMessageLike[]): OutlineMessage[] {
  const questions: OutlineMessage[] = []

  let current: OutlineMessage | undefined
  for (const message of messages) {
    if (message.role === 'user') current = undefined
    if ((message.role !== 'user' && message.role !== 'assistant') || !message.id) continue

    if (message.role === 'assistant' && current) {
      current.messageIds.push(message.id)
      if (message.content) {
        current.answerContent = [current.answerContent, message.content].filter(Boolean).join('\n\n')
      }
      continue
    }

    current = {
      id: message.id,
      role: message.role,
      content: message.content ?? '',
      hasAttachments:
        (message.images?.length ?? 0) > 0 || (message.attachments?.length ?? 0) > 0,
      answerContent: '',
      messageIds: [message.id],
    }
    questions.push(current)
  }

  return questions
}

type MessageBounds = { id: string; offsetTop: number; offsetBottom: number }

/** One rail tick per turn, retaining each message's bounds for visibility. */
export function groupOutlineBounds(
  turns: OutlineMessage[],
  messages: MessageBounds[],
): Array<MessageBounds & { ranges: MessageBounds[] }> {
  const byId = new Map(messages.map(message => [message.id, message]))
  return turns.flatMap(turn => {
    const ranges = turn.messageIds.flatMap(id => {
      const bounds = byId.get(id)
      return bounds && bounds.offsetBottom > bounds.offsetTop ? [bounds] : []
    })
    if (!ranges.length) return []
    return [{
      id: turn.id,
      offsetTop: Math.min(...ranges.map(range => range.offsetTop)),
      offsetBottom: Math.max(...ranges.map(range => range.offsetBottom)),
      ranges,
    }]
  })
}

/** Include partially visible messages and long answers spanning the viewport. */
export function visibleMessageIds(
  items: Array<MessageBounds & { ranges?: MessageBounds[] }>,
  scrollTop: number,
  clientHeight: number,
  bottomInset: number = 0,
): Set<string> {
  const visibleHeight = Math.max(0, clientHeight - bottomInset)
  if (visibleHeight <= 0) return new Set()
  const bottom = scrollTop + visibleHeight
  return new Set(items.filter(item => (item.ranges ?? [item]).some(range => (
    range.offsetBottom > range.offsetTop && range.offsetBottom > scrollTop && range.offsetTop < bottom
  ))).map(item => item.id))
}

export function questionDisplayText(
  content: string | undefined,
  attachmentPlaceholder: string,
): string {
  const normalized = (content ?? '').replace(/\s+/g, ' ').trim()
  return normalized.length > 0 ? normalized : attachmentPlaceholder
}

export function answerPreviewText(content: string | undefined): string {
  const normalized = (content ?? '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[#>*_~-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  return normalized
}

export function tickMountainScale(tickY: number, pointerY: number | null): number {
  if (pointerY === null) return 1
  const dy = tickY - pointerY
  const sigma = QUESTION_TICK_GAP_PX
  return 1 + QUESTION_TICK_MOUNTAIN_GAIN * Math.exp(-0.5 * (dy / sigma) ** 2)
}

export function tickDisplayScale(
  tickY: number,
  pointerY: number | null,
  isCurrent: boolean,
): number {
  if (pointerY !== null) return tickMountainScale(tickY, pointerY)
  return isCurrent ? CURRENT_TICK_SCALE : 1
}

export function nearestTickId(
  ticks: Array<{ id: string; yPx: number }>,
  pointerY: number,
): string | null {
  if (ticks.length === 0) return null

  let bestId = ticks[0].id
  let bestDist = Math.abs(ticks[0].yPx - pointerY)
  for (let index = 1; index < ticks.length; index++) {
    const dist = Math.abs(ticks[index].yPx - pointerY)
    if (dist < bestDist) {
      bestDist = dist
      bestId = ticks[index].id
    }
  }
  return bestId
}

export function offsetFromScrollContent(
  anchorTop: number,
  containerTop: number,
  scrollTop: number,
): number {
  return anchorTop - containerTop + scrollTop
}

export function mapQuestionTicks(
  items: Array<{ id: string; offsetTop?: number }>,
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
  const minY = Math.min(QUESTION_TICK_INSET_PX, trackHeight / 2)
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
