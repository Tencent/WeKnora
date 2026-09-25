/**
 * 「刷新」语义的请求排序器（/auth/me 这类单例读取用）。
 *
 * - `refresh()`：结果必须来自本次调用之后发出的请求。已有请求在飞行时，
 *   排一个新请求在它之后，多次并发的 refresh 共用同一个排队请求。
 * - `share()`：只要有请求在飞行就直接复用，否则发一个新的。首屏多个消费者
 *   同时要同一份数据时用它。
 * - `invalidate()`：让飞行中的请求作废。`run` 拿到的 `isCurrent()` 在 await 之后
 *   返回 false，调用方据此跳过落库；登出时用，避免迟到的响应把会话写回来。
 */
export interface SequencedRefresh<T> {
  refresh: () => Promise<T>
  share: () => Promise<T>
  invalidate: () => void
  hasInFlightRequest: () => boolean
}

export function createSequencedRefresh<T>(
  run: (isCurrent: () => boolean) => Promise<T>,
): SequencedRefresh<T> {
  let revision = 0
  let inflight: Promise<T> | null = null
  let queued: Promise<T> | null = null

  const start = (): Promise<T> => {
    const requestRevision = revision
    const current = run(() => requestRevision === revision).finally(() => {
      if (inflight === current) inflight = null
    })
    inflight = current
    return current
  }

  const refresh = (): Promise<T> => {
    if (!inflight) return start()
    if (!queued) {
      const waitingFor = inflight
      queued = waitingFor.then(
        () => {
          queued = null
          return start()
        },
        () => {
          queued = null
          return start()
        },
      )
    }
    return queued
  }

  return {
    refresh,
    share: () => inflight ?? start(),
    invalidate: () => {
      revision += 1
    },
    hasInFlightRequest: () => inflight !== null,
  }
}
