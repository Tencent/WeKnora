import { shallowReactive } from 'vue'

/** Anything a registry holds is addressed by a unique key. */
export interface Keyed {
  key: string
}

/**
 * An extension registry: builtins register at startup, plugins at runtime,
 * and the UI renders whatever is registered. Reads are reactive, so a plugin
 * enabled later shows up without a reload.
 */
export interface Registry<T extends Keyed> {
  /** Adds an item; returns a function that removes it again. */
  register(item: T): () => void
  unregister(key: string): void
  get(key: string): T | undefined
  /** Every registered item, in registration order. */
  readonly items: readonly T[]
}

export function createRegistry<T extends Keyed>(name: string): Registry<T> {
  const items = shallowReactive<T[]>([])
  const unregister = (key: string) => {
    const i = items.findIndex((item) => item.key === key)
    if (i >= 0) items.splice(i, 1)
  }
  return {
    register(item) {
      if (items.some((existing) => existing.key === item.key)) {
        throw new Error(`${name} "${item.key}" is already registered`)
      }
      items.push(item)
      return () => unregister(item.key)
    },
    unregister,
    get: (key) => items.find((item) => item.key === key),
    get items() {
      return items
    },
  }
}
