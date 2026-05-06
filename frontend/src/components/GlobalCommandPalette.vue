<template>
  <t-dialog
    v-model:visible="dialogVisible"
    :footer="false"
    :header="false"
    :close-btn="false"
    width="640px"
    top="10vh"
    destroy-on-close
    class="cmdk-dialog"
    @close="handleClose"
    @opened="onDialogOpened"
  >
    <div class="cmdk" @keydown="onKeyDown">
      <!-- Input row -->
      <div class="cmdk__input-row">
        <t-icon name="search" class="cmdk__input-icon" />
        <span v-if="activeKbScope" class="cmdk__scope-chip" :title="activeKbScope.name">
          <t-icon name="folder" size="12px" />
          <span class="cmdk__scope-chip-name">{{ activeKbScope.name }}</span>
          <button
            type="button"
            class="cmdk__scope-chip-x"
            :title="t('commandPalette.scope.remove')"
            :aria-label="t('commandPalette.scope.remove')"
            @click="clearKbScope"
          >
            <t-icon name="close" size="12px" />
          </button>
        </span>
        <input
          ref="inputRef"
          v-model="query"
          type="text"
          class="cmdk__input"
          :placeholder="activeKbScope ? t('commandPalette.scope.placeholder') : t('commandPalette.placeholder')"
          autofocus
          spellcheck="false"
          @keydown="onInputKeyDown"
        />
        <span v-if="loading" class="cmdk__input-spinner">
          <t-loading size="small" />
        </span>
        <t-tooltip :content="t('commandPalette.retrieval')" placement="bottom">
          <button
            type="button"
            class="cmdk__icon-btn"
            :class="{ active: drawerVisible }"
            @click="drawerVisible = true"
          >
            <t-icon name="setting" size="16px" />
          </button>
        </t-tooltip>
        <button
          type="button"
          class="cmdk__icon-btn"
          :aria-label="t('commandPalette.hotkey.esc')"
          @click="handleClose"
        >
          <t-icon name="close" size="16px" />
        </button>
      </div>

      <!-- Results -->
      <div ref="scrollRef" class="cmdk__results">
        <!-- Empty idle state: recent + quick actions -->
        <template v-if="!query.trim()">
          <ResultGroup
            v-if="recentQueries.length"
            :label="t('commandPalette.group.recent')"
            :action="t('commandPalette.clearRecent')"
            @action="commandPaletteStore.clearRecent()"
          >
            <ResultItem
              v-for="(q, i) in recentQueries"
              :key="'r-' + q"
              icon-name="history"
              :index="flatIndexFor('recent', i)"
              :selected="selectedIndex === flatIndexFor('recent', i)"
              :shortcut="shortcutFor(flatIndexFor('recent', i))"
              :title="q"
              @primary="query = q"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>

          <ResultGroup :label="t('commandPalette.group.quickActions')">
            <ResultItem
              v-for="(c, i) in allCommands"
              :key="'cmd-' + c.id"
              :index="flatIndexFor('commands', i)"
              :selected="selectedIndex === flatIndexFor('commands', i)"
              :shortcut="shortcutFor(flatIndexFor('commands', i))"
              :icon-name="c.icon"
              :title="c.label"
              @primary="c.run"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>
        </template>

        <!-- Active search results -->
        <template v-else>
          <!-- Chunks (per file) -->
          <ResultGroup
            v-if="isGroupVisible('chunks') && fileGroups.length"
            :label="t('commandPalette.group.chunks')"
            :count="totalChunks"
          >
            <template v-for="(item, i) in flatChunkItems" :key="'c-' + item.file.knowledgeId + '-' + item.chunk.id">
              <ResultItem
                :index="flatIndexFor('chunks', i)"
                :selected="selectedIndex === flatIndexFor('chunks', i)"
                :shortcut="shortcutFor(flatIndexFor('chunks', i))"
                icon-name="file"
                :badge="item.chunk.match_type === 'vector' ? t('commandPalette.match.vector') : t('commandPalette.match.keyword')"
                :badge-variant="item.chunk.match_type === 'vector' ? 'vector' : 'keyword'"
                :score="item.chunk.score"
                @primary="openChunk(item)"
                @hover="selectItemAt($event)"
              >
                <template #title>
                  <span class="cmdk-chunk-title">{{ item.file.title }}</span>
                  <span v-if="item.file.kbName" class="cmdk-chunk-kb">{{ item.file.kbName }}</span>
                </template>
                <template #subtitle>
                  <span v-html="highlight(item.chunk.matched_content || item.chunk.content)" />
                </template>
              </ResultItem>
            </template>
          </ResultGroup>

          <!-- Messages -->
          <ResultGroup
            v-if="isGroupVisible('messages') && messageGroups.length"
            :label="t('commandPalette.group.messages')"
            :count="totalMessages"
          >
            <template v-for="(item, i) in flatMessageItems" :key="'m-' + item.msg.request_id">
              <ResultItem
                :index="flatIndexFor('messages', i)"
                :selected="selectedIndex === flatIndexFor('messages', i)"
                :shortcut="shortcutFor(flatIndexFor('messages', i))"
                icon-name="chat"
                :score="item.msg.score"
                @primary="openMessage(item)"
                @hover="selectItemAt($event)"
              >
                <template #title>
                  <span>{{ item.group.sessionTitle || t('commandPalette.untitledSession') }}</span>
                </template>
                <template #subtitle>
                  <span class="cmdk-msg-role">{{ item.msg.query_content ? 'Q' : 'A' }}</span>
                  <span v-html="highlight(item.msg.query_content || item.msg.answer_content)" />
                </template>
              </ResultItem>
            </template>
          </ResultGroup>

          <!-- KB name matches -->
          <ResultGroup
            v-if="isGroupVisible('kbs') && kbMatches.length"
            :label="t('commandPalette.group.kbs')"
          >
            <ResultItem
              v-for="(kb, i) in kbMatches"
              :key="'k-' + kb.id"
              :index="flatIndexFor('kbs', i)"
              :selected="selectedIndex === flatIndexFor('kbs', i)"
              :shortcut="shortcutFor(flatIndexFor('kbs', i))"
              icon-name="folder"
              :title="kb.name"
              @primary="openKb(kb.id)"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>

          <!-- Agent matches -->
          <ResultGroup
            v-if="isGroupVisible('agents') && agentMatches.length"
            :label="t('commandPalette.group.agents')"
          >
            <ResultItem
              v-for="(a, i) in agentMatches"
              :key="'a-' + a.id"
              :index="flatIndexFor('agents', i)"
              :selected="selectedIndex === flatIndexFor('agents', i)"
              :shortcut="shortcutFor(flatIndexFor('agents', i))"
              icon-name="user-circle"
              :title="a.name"
              :subtitle="a.description"
              @primary="openAgent(a.id)"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>

          <!-- Session (chat) title matches -->
          <ResultGroup
            v-if="isGroupVisible('sessions') && sessionMatches.length"
            :label="t('commandPalette.group.sessionsByTitle')"
          >
            <ResultItem
              v-for="(s, i) in sessionMatches"
              :key="'s-' + s.id"
              :index="flatIndexFor('sessions', i)"
              :selected="selectedIndex === flatIndexFor('sessions', i)"
              :shortcut="shortcutFor(flatIndexFor('sessions', i))"
              icon-name="chat"
              :title="s.title"
              @primary="openSession(s.id)"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>

          <!-- Commands matching the query -->
          <ResultGroup
            v-if="isGroupVisible('commands') && filteredCommands.length"
            :label="t('commandPalette.group.commands')"
          >
            <ResultItem
              v-for="(c, i) in filteredCommands"
              :key="'fcmd-' + c.id"
              :index="flatIndexFor('commands', i)"
              :selected="selectedIndex === flatIndexFor('commands', i)"
              :shortcut="shortcutFor(flatIndexFor('commands', i))"
              :icon-name="c.icon"
              :title="c.label"
              @primary="c.run"
              @hover="selectItemAt($event)"
            />
          </ResultGroup>

          <!-- No results -->
          <div
            v-if="!loading && !hasAnyResults && hasSearched"
            class="cmdk__empty"
          >
            <p>{{ t('commandPalette.empty.noResults') }}</p>
            <div class="cmdk__empty-actions">
              <t-button theme="primary" variant="outline" size="small" @click="askAi">
                <template #icon><t-icon name="chat" size="14px" /></template>
                {{ t('commandPalette.empty.askAi') }}
              </t-button>
              <t-button variant="outline" size="small" @click="drawerVisible = true">
                <template #icon><t-icon name="setting" size="14px" /></template>
                {{ t('commandPalette.empty.adjustRetrieval') }}
              </t-button>
            </div>
          </div>
        </template>
      </div>

      <!-- Hotkey footer -->
      <div class="cmdk__footer">
        <span class="cmdk__hotkey"><kbd>↑</kbd><kbd>↓</kbd> {{ t('commandPalette.hotkey.select') }}</span>
        <span class="cmdk__hotkey"><kbd>↵</kbd> {{ t('commandPalette.hotkey.enter') }}</span>
        <span class="cmdk__hotkey"><kbd>⌘</kbd><kbd>1</kbd>-<kbd>9</kbd> {{ t('commandPalette.hotkey.cmdNumber') }}</span>
        <span class="cmdk__hotkey"><kbd>⌘</kbd><kbd>↵</kbd> {{ t('commandPalette.hotkey.cmdEnter') }}</span>
        <span class="cmdk__hotkey"><kbd>Esc</kbd> {{ t('commandPalette.hotkey.esc') }}</span>
      </div>
    </div>

    <!-- Retrieval settings drawer (layered on top of the palette) -->
    <t-drawer
      v-model:visible="drawerVisible"
      :header="t('retrievalSettings.title')"
      size="420px"
      :footer="false"
      :close-on-overlay-click="true"
      class="cmdk-retrieval-drawer"
    >
      <RetrievalSettings />
    </t-drawer>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useCommandPaletteStore } from '@/stores/commandPalette'
import { useCmdkSearch, type CmdkFileGroup, type CmdkChunk, type CmdkMsgGroup } from './GlobalCommandPalette/useSearch'
import { highlightText } from './GlobalCommandPalette/useHighlight'
import { useStartChat } from './GlobalCommandPalette/useStartChat'
import { buildCommands, filterCommands } from './GlobalCommandPalette/commands'
import ResultGroup from './GlobalCommandPalette/ResultGroup.vue'
import ResultItem from './GlobalCommandPalette/ResultItem.vue'
import RetrievalSettings from '@/views/settings/RetrievalSettings.vue'
import type { MessageSearchGroupItem } from '@/api/chat-history'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const commandPaletteStore = useCommandPaletteStore()
const { open, initialQuery, recentQueries } = storeToRefs(commandPaletteStore)
const { startChat } = useStartChat()

// Per-group display caps — keep the palette compact.
const CHUNK_LIMIT = 5
const MSG_LIMIT = 4

// Active KB scope: when the palette opens on a KB detail page, we default to
// searching within that KB. The user can remove the chip to expand scope.
const activeKbScope = ref<{ id: string; name: string } | null>(null)
const scopeDismissed = ref(false) // user clicked ✕, remember for this session

const {
  query, loading, hasSearched,
  fileGroups, messageGroups, kbMatches,
  knowledgeBases,
  agentMatches, sessionMatches,
  totalChunks, totalMessages,
  clearResults,
} = useCmdkSearch({
  lockedKbIds: () => (activeKbScope.value ? [activeKbScope.value.id] : []),
})

const drawerVisible = ref(false)
const inputRef = ref<HTMLInputElement | null>(null)
const scrollRef = ref<HTMLElement | null>(null)

// Selection is identity-based (by item key) rather than positional, so
// results arriving asynchronously (e.g. slow KB search) can't silently
// reassign the user's highlight to a different row. `selectedIndex` is
// derived: it's the current position of `selectedKey` in flatItems, or 0 if
// the selected item is gone.
const selectedKey = ref<string | null>(null)

// Proxy the store's `open` to t-dialog v-model.
const dialogVisible = computed<boolean>({
  get: () => open.value,
  set: (v) => {
    if (!v) handleClose()
  },
})

// Flatten visible items into one indexable list for keyboard nav.
interface FlatChunkItem { kind: 'chunk'; file: CmdkFileGroup; chunk: CmdkChunk }
interface FlatMsgItem { kind: 'msg'; group: CmdkMsgGroup; msg: MessageSearchGroupItem }

const flatChunkItems = computed<FlatChunkItem[]>(() => {
  const out: FlatChunkItem[] = []
  for (const f of fileGroups.value) {
    for (const c of f.chunks) {
      out.push({ kind: 'chunk', file: f, chunk: c })
      if (out.length >= CHUNK_LIMIT) return out
    }
  }
  return out
})

const flatMessageItems = computed<FlatMsgItem[]>(() => {
  const out: FlatMsgItem[] = []
  for (const g of messageGroups.value) {
    for (const m of g.items) {
      out.push({ kind: 'msg', group: g, msg: m })
      if (out.length >= MSG_LIMIT) return out
    }
  }
  return out
})

// ─── Commands ───
// All palette commands live in a shared module so quick-action (empty state)
// and the "commands" tab can use the same data.
const allCommands = computed(() =>
  buildCommands({
    router,
    t,
    close: () => commandPaletteStore.closePalette(),
  }),
)

const filteredCommands = computed(() => filterCommands(allCommands.value, query.value))

// Stable index layout — keep group order consistent so flatIndexFor is cheap.
// Groups always render in the same order; individual groups hide themselves
// when their backing list is empty, so users with nothing to show don't see
// empty headers.
const groupOrder = computed<readonly string[]>(() => {
  if (!query.value.trim()) {
    // Empty state: recents + full command list.
    return ['recent', 'commands'] as const
  }
  if (activeKbScope.value) {
    // Scoped to one KB: only chunks make sense (messages disabled in useSearch).
    return ['chunks'] as const
  }
  return ['chunks', 'messages', 'kbs', 'agents', 'sessions', 'commands'] as const
})

const groupSizes = computed<Record<string, number>>(() => ({
  recent: recentQueries.value.length,
  commands: query.value.trim() ? filteredCommands.value.length : allCommands.value.length,
  chunks: flatChunkItems.value.length,
  messages: flatMessageItems.value.length,
  kbs: kbMatches.value.length,
  agents: agentMatches.value.length,
  sessions: sessionMatches.value.length,
}))

const flatIndexFor = (group: string, localIndex: number): number => {
  let base = 0
  for (const g of groupOrder.value) {
    if (g === group) return base + localIndex
    base += groupSizes.value[g] || 0
  }
  return base + localIndex
}

/**
 * Unified flattened list driving keyboard nav, ⌘N shortcuts and the primary
 * action. Each entry carries a stable `key` (so selection survives async
 * result arrivals) plus a `run` callback. Order follows `groupOrder`.
 */
interface FlatItem {
  key: string
  group: string
  run: (ev?: { cmd: boolean }) => void
}

const flatItems = computed<FlatItem[]>(() => {
  const out: FlatItem[] = []
  for (const g of groupOrder.value) {
    if (g === 'recent') {
      recentQueries.value.forEach((q, i) => {
        out.push({
          key: `recent:${i}:${q}`,
          group: g,
          run: () => { query.value = q },
        })
      })
    } else if (g === 'commands') {
      const list = query.value.trim() ? filteredCommands.value : allCommands.value
      list.forEach((cmd) => {
        out.push({ key: `cmd:${cmd.id}`, group: g, run: () => cmd.run() })
      })
    } else if (g === 'chunks') {
      flatChunkItems.value.forEach((item) => {
        out.push({
          key: `chunk:${item.chunk.id}`,
          group: g,
          run: (ev) => (ev?.cmd ? cmdEnterChunk(item) : openChunk(item)),
        })
      })
    } else if (g === 'messages') {
      flatMessageItems.value.forEach((item) => {
        out.push({
          key: `msg:${item.msg.request_id}`,
          group: g,
          run: () => openMessage(item),
        })
      })
    } else if (g === 'kbs') {
      kbMatches.value.forEach((kb) => {
        out.push({ key: `kb:${kb.id}`, group: g, run: () => openKb(kb.id) })
      })
    } else if (g === 'agents') {
      agentMatches.value.forEach((a) => {
        out.push({ key: `agent:${a.id}`, group: g, run: () => openAgent(a.id) })
      })
    } else if (g === 'sessions') {
      sessionMatches.value.forEach((s) => {
        out.push({ key: `session:${s.id}`, group: g, run: () => openSession(s.id) })
      })
    }
  }
  return out
})

const totalItems = computed(() => flatItems.value.length)

/** Derived: position of the currently-selected item, or 0 if it no longer exists. */
const selectedIndex = computed<number>(() => {
  if (!selectedKey.value) return 0
  const idx = flatItems.value.findIndex((it) => it.key === selectedKey.value)
  return idx >= 0 ? idx : 0
})

const hasAnyResults = computed(() => {
  // "Any result" means at least one of the groups visible under the current
  // state has items. The empty-state message should only appear when the user
  // really has nothing to click on.
  for (const g of groupOrder.value) {
    if ((groupSizes.value[g] || 0) > 0) return true
  }
  return false
})

/** Whether a named group should be rendered under the current state. */
const isGroupVisible = (name: string): boolean => groupOrder.value.includes(name as never)

// ─── Navigation helpers ───

const primaryActionForSelected = (ev?: { cmd: boolean }) => {
  const item = flatItems.value[selectedIndex.value]
  item?.run(ev)
}

const openChunk = (item: FlatChunkItem) => {
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  if (!item.file.kbId) return
  const currentKbId = typeof route.params.kbId === 'string' ? route.params.kbId : ''
  // If the user is already on this KB page, router.push to the same path+query
  // is a no-op (vue-router dedupes identical navigations), so the document
  // auto-open logic never fires. Dispatch a global event that KnowledgeBase.vue
  // listens for instead; this also avoids reloading the KB list on every click.
  if (currentKbId === item.file.kbId) {
    window.dispatchEvent(
      new CustomEvent('weknora:open-knowledge', {
        detail: { kbId: item.file.kbId, knowledgeId: item.file.knowledgeId },
      }),
    )
    return
  }
  router.push({
    path: `/platform/knowledge-bases/${item.file.kbId}`,
    query: { knowledge_id: item.file.knowledgeId },
  })
}

const cmdEnterChunk = (item: FlatChunkItem) => {
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  const kbIds = item.file.kbId ? [item.file.kbId] : []
  startChat(query.value, kbIds, [item.file.knowledgeId])
}

const openMessage = (item: FlatMsgItem) => {
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  if (item.group.sessionId) {
    router.push(`/platform/chat/${item.group.sessionId}`)
  }
}

const openKb = (kbId: string) => {
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  router.push(`/platform/knowledge-bases/${kbId}`)
}

// Agents have no standalone detail route; the natural "jump" is opening a new
// chat pre-scoped to that agent — the agent editor is a modal that requires
// more context (permissions, edit intent) so we don't trigger it from ⌘K.
const openAgent = (agentId: string) => {
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  router.push({ path: '/platform/creatChat', query: { agent_id: agentId } })
}

const openSession = (sessionId: string) => {
  if (!sessionId) return
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  router.push(`/platform/chat/${sessionId}`)
}

const askAi = () => {
  if (!query.value.trim()) return
  commandPaletteStore.pushRecent(query.value)
  commandPaletteStore.closePalette()
  startChat(query.value)
}

const highlight = (text: string) => highlightText(text, query.value)

// ─── Keyboard navigation ───

const scrollSelectedIntoView = () => {
  nextTick(() => {
    const el = scrollRef.value?.querySelector<HTMLElement>(`[data-cmdk-index="${selectedIndex.value}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  })
}

const moveSelection = (delta: number) => {
  const total = totalItems.value
  if (total === 0) return
  const next = (selectedIndex.value + delta + total) % total
  selectedKey.value = flatItems.value[next]?.key || null
  scrollSelectedIntoView()
}

const selectItemAt = (idx: number) => {
  const item = flatItems.value[idx]
  if (!item) return
  selectedKey.value = item.key
}

/**
 * Return the ⌘N shortcut digit for the item at the given flat index, or
 * undefined if it's past the 9th slot. The ResultItem renders a kbd hint
 * iff this is defined, which is the only cue the user has that ⌘1-9 works.
 */
const shortcutFor = (flatIndex: number): number | undefined => {
  if (flatIndex < 0 || flatIndex > 8) return undefined
  return flatIndex + 1
}

const onKeyDown = (e: KeyboardEvent) => {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    moveSelection(1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    moveSelection(-1)
  } else if (
    (e.metaKey || e.ctrlKey) &&
    e.key >= '1' && e.key <= '9'
  ) {
    // ⌘1-9 — jump straight to the Nth visible item (shortcut badge on each
    // row reveals the binding). No modifier combos with ⌘Enter: digits take
    // precedence because ⌘+digit can't be confused with ⌘+enter.
    const n = parseInt(e.key, 10)
    const item = flatItems.value[n - 1]
    if (item) {
      e.preventDefault()
      item.run({ cmd: false })
    }
  } else if (e.key === 'Enter') {
    e.preventDefault()
    primaryActionForSelected({ cmd: e.metaKey || e.ctrlKey })
  } else if (e.key === 'Escape') {
    e.preventDefault()
    handleClose()
  }
}

// Keys handled specifically on the input element. Main purpose: let the user
// escape a KB scope chip with the keyboard. Mirrors how chat apps treat
// pill/tag tokens — Backspace on an empty input removes the preceding chip.
const onInputKeyDown = (e: KeyboardEvent) => {
  if (
    e.key === 'Backspace' &&
    !query.value &&
    activeKbScope.value
  ) {
    e.preventDefault()
    clearKbScope()
  }
}

const handleClose = () => {
  drawerVisible.value = false
  commandPaletteStore.closePalette()
}

// ─── Global ⌘K shortcut ───

const isEditingElement = (el: EventTarget | null): boolean => {
  if (!el) return false
  const node = el as HTMLElement
  const tag = (node.tagName || '').toUpperCase()
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (node.isContentEditable) return true
  return false
}

const onGlobalKey = (e: KeyboardEvent) => {
  const isCmd = e.metaKey || e.ctrlKey
  if (isCmd && e.key.toLowerCase() === 'k') {
    // Always allow ⌘K to open, even from an input.
    e.preventDefault()
    if (open.value) handleClose()
    else commandPaletteStore.openPalette('')
    return
  }
  // Plain "/" opens the palette when nothing is focused.
  if (e.key === '/' && !open.value && !isEditingElement(e.target)) {
    e.preventDefault()
    commandPaletteStore.openPalette('')
  }
}

// When the palette opens programmatically (from router redirect or elsewhere),
// sync the query and initialize scope. Focus is moved to the input by
// `onDialogOpened` once the dialog's enter animation has finished — doing it
// here in a plain `nextTick` is too early for t-dialog (the input may not be
// attached to the document yet, so `.focus()` silently no-ops).
watch(open, (val) => {
  if (val) {
    query.value = initialQuery.value || ''
    selectedKey.value = null
    // Infer KB scope from current route. Reset scopeDismissed on each open so
    // the default behavior is "scope to current KB unless user clicks ✕".
    scopeDismissed.value = false
    const kbIdFromRoute = typeof route.params.kbId === 'string' ? route.params.kbId : ''
    if (kbIdFromRoute) {
      // Best-effort name resolution; falls back to short id.
      const match = knowledgeBases.value.find((k) => k.id === kbIdFromRoute)
      activeKbScope.value = {
        id: kbIdFromRoute,
        name: match?.name || kbIdFromRoute,
      }
    } else {
      activeKbScope.value = null
    }
  } else {
    clearResults()
    query.value = ''
    drawerVisible.value = false
    activeKbScope.value = null
  }
})

// Fired by t-dialog after its open animation finishes — DOM is guaranteed
// attached, focus sticks. We also schedule a couple of retries because
// some browsers defer focus when the dialog is mid-layout; costs nothing.
const focusInputWithRetry = () => {
  const tryFocus = () => {
    const el = inputRef.value
    if (!el) return false
    el.focus()
    // Move cursor to end so an initialQuery is editable, not overwritten.
    if (typeof el.setSelectionRange === 'function' && el.value) {
      const len = el.value.length
      try { el.setSelectionRange(len, len) } catch { /* not all input types support this */ }
    }
    return document.activeElement === el
  }
  if (tryFocus()) return
  requestAnimationFrame(() => {
    if (tryFocus()) return
    setTimeout(tryFocus, 50)
  })
}

const onDialogOpened = () => {
  focusInputWithRetry()
}

// Once KB list loads, backfill the scope name if we only had an id at open time.
watch(knowledgeBases, (list) => {
  if (activeKbScope.value && activeKbScope.value.name === activeKbScope.value.id) {
    const match = list.find((k) => k.id === activeKbScope.value!.id)
    if (match) activeKbScope.value = { id: match.id, name: match.name }
  }
})

const clearKbScope = () => {
  activeKbScope.value = null
  scopeDismissed.value = true
  nextTick(() => inputRef.value?.focus())
}

onMounted(() => {
  window.addEventListener('keydown', onGlobalKey)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onGlobalKey)
})
</script>

<style lang="less" scoped>
.cmdk {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-height: 320px;
  max-height: 60vh;
}

.cmdk__input-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.cmdk__input-icon {
  color: var(--td-text-color-placeholder);
  font-size: 16px;
}

.cmdk__scope-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 220px;
  height: 26px;
  padding: 0 2px 0 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  flex-shrink: 0;

  :deep(.t-icon:first-child) {
    color: var(--td-text-color-secondary);
  }
}

.cmdk__scope-chip-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cmdk__scope-chip-x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  line-height: 1;
  padding: 0;

  :deep(svg) {
    width: 12px;
    height: 12px;
    display: block;
  }

  &:hover {
    color: var(--td-text-color-primary);
    background: rgba(0, 0, 0, 0.05);
  }
}

.cmdk__input {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  font-size: 15px;
  color: var(--td-text-color-primary);
  font-family: inherit;

  &::placeholder {
    color: var(--td-text-color-placeholder);
  }
}

.cmdk__input-spinner {
  display: flex;
  align-items: center;
}

.cmdk__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background 0.1s;

  &:hover,
  &.active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.cmdk__results {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 4px;
}

.cmdk__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 40px 20px 20px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;

  p {
    margin: 0;
  }
}

.cmdk__empty-actions {
  display: flex;
  gap: 8px;
}

.cmdk__footer {
  display: flex;
  gap: 16px;
  padding: 8px 14px;
  border-top: 1px solid var(--td-component-stroke);
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  flex-wrap: wrap;
}

.cmdk__hotkey {
  display: inline-flex;
  align-items: center;
  gap: 4px;

  kbd {
    display: inline-block;
    padding: 1px 5px;
    min-width: 16px;
    font-size: 10px;
    font-family: inherit;
    line-height: 14px;
    text-align: center;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: 3px;
    color: var(--td-text-color-secondary);
  }
}

.cmdk-chunk-kb {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  padding: 1px 6px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 3px;
  font-weight: 400;
}

.cmdk-chunk-title {
  overflow: hidden;
  text-overflow: ellipsis;
}

.cmdk-msg-role {
  display: inline-block;
  margin-right: 6px;
  padding: 0 5px;
  font-size: 10px;
  font-weight: 600;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  border-radius: 3px;
}
</style>

<style lang="less">
/* Unscoped overrides — dialog renders in teleport. */
.cmdk-dialog {
  .t-dialog__body {
    padding: 0;
  }

  .t-dialog {
    padding: 0;
    overflow: hidden;
  }
}

.cmdk-retrieval-drawer {
  .section-header {
    font-weight: 600;
  }
}
</style>
