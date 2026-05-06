import { defineStore } from 'pinia'
import { ref } from 'vue'

const RECENT_KEY = 'weknora_cmdk_recent'
const RECENT_LIMIT = 4

/**
 * Pinia store for the global command palette (⌘K / Ctrl+K).
 * The palette itself is rendered once in platform/index.vue.
 * Any code (router redirect, deep link, programmatic trigger) can open it
 * by calling openPalette(q).
 */
export const useCommandPaletteStore = defineStore('commandPalette', () => {
  const open = ref(false)
  const initialQuery = ref('')
  const recentQueries = ref<string[]>([])

  const loadRecent = () => {
    try {
      const raw = localStorage.getItem(RECENT_KEY)
      recentQueries.value = raw ? JSON.parse(raw) : []
    } catch {
      recentQueries.value = []
    }
  }

  const openPalette = (query = '') => {
    initialQuery.value = query
    open.value = true
  }

  const closePalette = () => {
    open.value = false
    initialQuery.value = ''
  }

  const pushRecent = (q: string) => {
    const trimmed = q.trim()
    if (!trimmed) return
    recentQueries.value = [
      trimmed,
      ...recentQueries.value.filter(x => x !== trimmed),
    ].slice(0, RECENT_LIMIT)
    try {
      localStorage.setItem(RECENT_KEY, JSON.stringify(recentQueries.value))
    } catch {
      /* ignore quota errors */
    }
  }

  const clearRecent = () => {
    recentQueries.value = []
    try {
      localStorage.removeItem(RECENT_KEY)
    } catch {
      /* ignore */
    }
  }

  // Load recent queries immediately on store creation.
  loadRecent()

  return {
    open,
    initialQuery,
    recentQueries,
    openPalette,
    closePalette,
    pushRecent,
    clearRecent,
    loadRecent,
  }
})
