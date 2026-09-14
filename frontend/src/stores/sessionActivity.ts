import { reactive, watch } from 'vue'
import { defineStore } from 'pinia'
import { getMessageList } from '@/api/chat/index'
import { useAuthStore } from './auth'
import { createSessionActivityState, type SessionActivity } from './sessionActivityState'

export const useSessionActivityStore = defineStore('sessionActivity', () => {
  const entries = reactive<Record<string, SessionActivity>>({})
  const activity = createSessionActivityState(entries, async sessionId => {
    const result = await getMessageList({ session_id: sessionId, limit: 20, created_at: '' })
    return result.data || []
  })
  const auth = useAuthStore()
  watch(() => [auth.user?.id, auth.effectiveTenantId], activity.clear, { flush: 'sync' })
  return { entries, ...activity }
})
