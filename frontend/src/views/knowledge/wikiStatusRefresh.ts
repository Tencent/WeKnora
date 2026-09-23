export type KnowledgePollStatus = {
  parse_status?: string
  summary_status?: string
}

export function isKnowledgeParseInFlight(status?: string): boolean {
  return status === 'pending' || status === 'processing' || status === 'finalizing'
}

export function knowledgeNeedsStatusPolling(item: KnowledgePollStatus): boolean {
  if (isKnowledgeParseInFlight(item.parse_status)) return true
  return item.parse_status === 'completed' &&
    (item.summary_status === 'pending' || item.summary_status === 'processing')
}

export function shouldRefreshWikiStatusAfterKnowledgePoll(
  before: KnowledgePollStatus,
  after: KnowledgePollStatus,
): boolean {
  return knowledgeNeedsStatusPolling(before) && !knowledgeNeedsStatusPolling(after)
}

export type PolledKnowledgeDetails = KnowledgePollStatus & {
  file_version?: number
  file_type?: string
  title?: string
}

// applyPolledKnowledgeDetails copies a status poll onto the open document.
// A newer file_version is adopted so the drawer follows a replacement instead
// of polling the previous source forever.
export function applyPolledKnowledgeDetails(
  current: PolledKnowledgeDetails,
  incoming: PolledKnowledgeDetails & { file_name?: string },
): { refreshDocument: boolean } {
  const versionChanged = Boolean(
    incoming.file_version &&
    current.file_version &&
    incoming.file_version !== current.file_version,
  )
  const wasProcessing = isKnowledgeParseInFlight(current.parse_status)
  if (incoming.parse_status) current.parse_status = incoming.parse_status
  if (typeof incoming.file_version === 'number' && incoming.file_version > 0) {
    current.file_version = incoming.file_version
  }
  if (incoming.file_type) current.file_type = incoming.file_type
  if (versionChanged) {
    const title = incoming.file_name || incoming.title
    if (title) current.title = title
  }
  return {
    refreshDocument: versionChanged || (wasProcessing && !isKnowledgeParseInFlight(current.parse_status)),
  }
}
