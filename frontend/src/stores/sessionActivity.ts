import { reactive, watch } from 'vue'
import { defineStore } from 'pinia'
import { getMessageList } from '@/api/chat/index'
import { useAuthStore } from './auth'
import {
  createSessionActivityState,
  restoreSessionActivity,
  serializeSessionActivity,
  type SessionActivity,
} from './sessionActivityState'

const STORAGE_PREFIX = 'weknora:session-activity'

export const useSessionActivityStore = defineStore('sessionActivity', () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const unread = reactive<Record<string, true>>({})
  const activity = createSessionActivityState(entries, async sessionId => {
    const result = await getMessageList({ session_id: sessionId, limit: 20, created_at: '' })
    return result.data || []
  }, unread)
  const auth = useAuthStore()

  const storageKey = () => {
    const userId = auth.user?.id
    return userId ? `${STORAGE_PREFIX}:${userId}:${auth.effectiveTenantId ?? ''}` : ''
  }

  // clear() only resets memory (unmount, account switch); writes happen after
  // real transitions so an unmount cannot wipe what the next mount restores.
  function persist() {
    const key = storageKey()
    if (!key) return
    try {
      localStorage.setItem(key, serializeSessionActivity(entries, unread))
    } catch {
      // Storage full or disabled: the in-memory markers still work.
    }
  }

  function restore() {
    const key = storageKey()
    if (!key) return
    try {
      restoreSessionActivity(localStorage.getItem(key), entries, unread)
    } catch {
      // Storage disabled.
    }
  }

  const persisting = <A extends unknown[], R>(fn: (...args: A) => R) => (...args: A): R => {
    const result = fn(...args)
    if (result instanceof Promise) void result.finally(persist)
    else persist()
    return result
  }

  watch(() => [auth.user?.id, auth.effectiveTenantId], () => {
    activity.clear()
    restore()
  }, { flush: 'sync' })
  restore()

  return {
    entries,
    unread,
    update: persisting(activity.update),
    detach: persisting(activity.detach),
    setViewing: persisting(activity.setViewing),
    dismiss: persisting(activity.dismiss),
    refresh: persisting(activity.refresh),
    clear: activity.clear,
    restore,
  }
})
