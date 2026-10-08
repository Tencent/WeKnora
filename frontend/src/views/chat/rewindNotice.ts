/**
 * Maps a rewind skip reason from the API to i18n copy.
 *
 * An empty reason means the workspace was reset; the caller should not show
 * a skip notice in that case.
 */
export type RewindCopyFn = (key: string) => string

export function rewindSkipMessage(reason: string, t: RewindCopyFn): string {
  if (!reason) {
    return ''
  }
  switch (reason) {
    case 'NO_SANDBOX':
      return t('chat.rewind.skipNoSandbox')
    case 'NO_CHECKPOINT':
      return t('chat.rewind.skipNoCheckpoint')
    default:
      return t('chat.rewind.skipped')
  }
}
