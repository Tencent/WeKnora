import { createVersionedRequestCoordinator } from './versionedRequest'

/**
 * 空间级资源的读取原语。不设 TTL：
 *
 * - `ensure()`：同一资源同一时刻只有一个请求在飞行，并发调用共用它；没有在飞行的
 *   就发一个新请求。返回的 Promise 在最新数据写入 store 后才 resolve，所以
 *   `await ensure()` 之后读到的一定是本次请求的结果，而不是旧快照。
 * - `ensure(true)`：写操作之后调用。若此刻有请求在飞行，会在其结束后再发一次，
 *   保证拿到的是写操作之后的数据。
 * - `invalidate()`：丢弃快照并让飞行中的旧响应作废；下一次 `ensure()` 必然发新请求。
 * - `markLoaded()`：调用方已经用别的接口拿到了最新列表并写回 store（如设置页
 *   `replaceModels`），把飞行中的旧请求作废，避免它把旧数据写回来。
 *
 * 旧快照仍然留在 store 的 ref 里供页面立即渲染；数据的新鲜度只由「是否刚发过请求」
 * 和显式失效决定，不再有 60s 内一律读缓存的窗口。
 */
export interface CachedResource {
  ensure: (force?: boolean) => Promise<void>
  invalidate: () => void
  markLoaded: () => void
  isLoaded: () => boolean
  hasInFlightRequest: () => boolean
}

export function createCachedResource<T>(
  request: () => Promise<T>,
  apply: (value: T) => void,
): CachedResource {
  let loaded = false
  // invalidate 之后若仍有旧请求在飞行，下一次 ensure 必须排在它之后重新发，
  // 否则调用方会 await 到一个结果已被作废的 Promise。
  let needsFreshRequest = false

  const coordinator = createVersionedRequestCoordinator(request, (value) => {
    apply(value)
    loaded = true
  })

  return {
    ensure(force = false) {
      const mustForce = force || needsFreshRequest
      needsFreshRequest = false
      return coordinator.fetch(mustForce)
    },
    invalidate() {
      loaded = false
      coordinator.invalidate()
      needsFreshRequest = coordinator.hasInFlightRequest()
    },
    markLoaded() {
      coordinator.invalidate()
      needsFreshRequest = false
      loaded = true
    },
    isLoaded: () => loaded,
    hasInFlightRequest: () => coordinator.hasInFlightRequest(),
  }
}

/**
 * 按 key 缓存的单条资源（知识库详情、智能体可见知识库等）。
 * 这类数据不随每次 ensure 重新拉取：拿到过就一直用，直到显式失效。
 * 调用方在循环里逐条 ensure（如 @ 提及列表补 count），不能每次都打接口。
 */
export function createKeyedSnapshotCache<T>(load: (key: string) => Promise<T>) {
  const snapshots = new Map<string, T>()
  const inflight = new Map<string, { promise: Promise<T>; revision: number }>()
  // 每个 key 一个代际；invalidate / force 都会推进它。请求返回时代际已变，
  // 说明中途发生过失效或有更新的请求，这次响应只交给调用方，不落快照。
  const revisions = new Map<string, number>()

  const bump = (key: string) => {
    const next = (revisions.get(key) ?? 0) + 1
    revisions.set(key, next)
    return next
  }

  return {
    async ensure(key: string, force = false): Promise<T> {
      if (!force && snapshots.has(key)) return snapshots.get(key) as T
      const existing = inflight.get(key)
      if (existing && !force) return existing.promise
      const revision = force ? bump(key) : (revisions.get(key) ?? 0)
      const promise = (async () => {
        try {
          const value = await load(key)
          const stillCurrent = (revisions.get(key) ?? 0) === revision
          // 加载失败以 null 表示时不落快照，下次读取会重试。
          if (stillCurrent && value != null) snapshots.set(key, value)
          return value
        } finally {
          if (inflight.get(key)?.revision === revision) inflight.delete(key)
        }
      })()
      inflight.set(key, { promise, revision })
      return promise
    },
    invalidate(key?: string) {
      if (key === undefined) {
        for (const k of new Set([...snapshots.keys(), ...inflight.keys()])) bump(k)
        snapshots.clear()
        inflight.clear()
        return
      }
      bump(key)
      snapshots.delete(key)
      inflight.delete(key)
    },
  }
}
