export function createSkillResourceCache<T>(request: (configId: string, sessionId?: string) => Promise<T>, ttl = 60_000) {
  const cached = new Map<string, { value: T; at: number }>()
  const pending = new Map<string, Promise<T>>()
  let generation = 0
  return {
    async get(configId: string, sessionId = '', force = false): Promise<T> {
      const key = JSON.stringify([configId, sessionId])
      const current = cached.get(key)
      if (!force && current && Date.now() - current.at < ttl) return current.value
      const existing = pending.get(key)
      if (existing) {
        if (!force) return existing
        await existing.catch(() => undefined)
        return this.get(configId, sessionId, true)
      }
      const revision = generation
      const promise = request(configId, sessionId).then(value => {
        if (revision === generation) cached.set(key, { value, at: Date.now() })
        return value
      }).finally(() => { if (pending.get(key) === promise) pending.delete(key) })
      pending.set(key, promise)
      return promise
    },
    clear() { generation++; cached.clear(); pending.clear() },
  }
}
