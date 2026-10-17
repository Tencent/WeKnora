export interface OneDriveOAuthPollStatus {
  authorized: boolean
  reauthorization_required: boolean
}

export interface OAuthPopupLike {
  closed: boolean
}

interface WaitForOneDriveAuthorizationOptions<T extends OneDriveOAuthPollStatus> {
  getStatus: () => Promise<T>
  onStatus: (status: T) => void
  popup: OAuthPopupLike | null
  wait: (milliseconds: number) => Promise<void>
  now?: () => number
  timeoutMs?: number
  popupCloseGraceMs?: number
  notCompletedMessage: string
}

export async function waitForOneDriveAuthorization<T extends OneDriveOAuthPollStatus>({
  getStatus,
  onStatus,
  popup,
  wait,
  now = Date.now,
  timeoutMs = 10 * 60 * 1000,
  popupCloseGraceMs = 5000,
  notCompletedMessage,
}: WaitForOneDriveAuthorizationOptions<T>): Promise<T> {
  const deadline = now() + timeoutMs
  let closedAt: number | undefined
  while (now() < deadline) {
    const status = await getStatus()
    onStatus(status)
    if (status.authorized && !status.reauthorization_required) {
      // The callback closes the popup after replacement cleanup finishes. A
      // blocked popup has no handle, so status is the only completion signal.
      if (popup && !popup.closed) {
        await wait(300)
        continue
      }
      return status
    }
    if (popup?.closed) {
      if (closedAt === undefined) closedAt = now()
      if (now() - closedAt > popupCloseGraceMs) break
    }
    await wait(800)
  }
  throw new Error(notCompletedMessage)
}
