<template>
  <div class="memory-settings">
    <div class="section-header">
      <div class="section-header-titlewrap">
        <h2>{{ t('memorySettings.title') }}</h2>
        <t-popup
          placement="bottom-start"
          trigger="hover"
          overlay-class-name="memory-usage-popup-overlay"
        >
          <button
            type="button"
            class="usage-trigger-btn"
            :aria-label="t('memorySettings.usage.iconHint')"
            :title="t('memorySettings.usage.iconHint')"
          >
            <t-icon name="info-circle" size="16px" />
          </button>
          <template #content>
            <div class="usage-popup">
              <div class="usage-popup-title">{{ t('memorySettings.usage.title') }}</div>
              <p class="usage-popup-intro">{{ t('memorySettings.usage.intro') }}</p>
              <div class="usage-popup-rows">
                <div v-for="key in usageRowKeys" :key="key" class="usage-popup-row">
                  <span class="usage-popup-label">{{ t(`memorySettings.usage.rows.${key}.label`) }}</span>
                  <span class="usage-popup-text">{{ t(`memorySettings.usage.rows.${key}.text`) }}</span>
                </div>
              </div>
            </div>
          </template>
        </t-popup>
      </div>
      <p class="section-description">{{ t('memorySettings.description') }}</p>
    </div>

    <!-- Workspace switch is off: say so plainly instead of showing a personal
         toggle that would appear to work and change nothing. -->
    <div v-if="settings && !settings.workspace_enabled" class="notice">
      <t-icon name="info-circle" />
      <span>{{ t('memorySettings.workspaceDisabled') }}</span>
    </div>

    <div class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ t('memorySettings.enableLabel') }}</label>
          <p class="desc">{{ t('memorySettings.enableDescription') }}</p>
          <!-- An agent can opt out on its own, so this switch being on is not a
               promise that every conversation uses memory. Say so here rather
               than letting someone conclude the page is broken. -->
          <p v-if="userEnabled && settings?.workspace_enabled" class="desc">
            {{ t('memorySettings.agentDisabledHint') }}
          </p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="userEnabled"
            :disabled="!settings || !settings.workspace_enabled"
            @change="handleEnabledChange"
          />
        </div>
      </div>
    </div>

    <div class="list-section">
      <div class="list-toolbar">
        <div class="list-title">
          <h3>{{ t('memorySettings.listTitle') }}</h3>
        </div>
        <div class="list-actions">
          <t-button size="small" variant="text" :disabled="isEmptyStore" @click="handleExport">
            <template #icon><t-icon name="download" /></template>
            {{ t('memorySettings.export') }}
          </t-button>
          <t-popconfirm
            :content="t('memorySettings.rewriteConfirm')"
            :confirm-btn="{ content: t('memorySettings.rewrite') }"
            :cancel-btn="t('common.cancel')"
            placement="bottom"
            @confirm="handleRewrite"
          >
            <t-button
              size="small"
              variant="text"
              :loading="rewriting"
              :disabled="!canWrite || episodeTotal === 0"
            >
              <template #icon><t-icon name="refresh" /></template>
              {{ t('memorySettings.rewrite') }}
            </t-button>
          </t-popconfirm>
          <t-popconfirm
            theme="danger"
            :content="t('memorySettings.clearConfirm')"
            :confirm-btn="{ content: t('memorySettings.clear'), theme: 'danger' }"
            :cancel-btn="t('common.cancel')"
            placement="left"
            @confirm="handleClear"
          >
            <t-button size="small" theme="danger" variant="text" :disabled="isEmptyStore">
              <template #icon><t-icon name="delete" /></template>
              {{ t('memorySettings.clear') }}
            </t-button>
          </t-popconfirm>
        </div>
      </div>

      <t-tabs :value="tab" class="status-tabs" @change="handleTabChange">
        <t-tab-panel v-for="value in tabs" :key="value" :value="value">
          <template #label>
            <span class="status-tab-label">
              <t-icon :name="tabIcon(value)" size="14px" />
              <span>{{ tabLabel(value) }}</span>
            </span>
          </template>
        </t-tab-panel>
      </t-tabs>

      <t-loading :loading="loading">
        <!-- Profile: one document the user corrects, so it gets a full-height
             editor rather than a row in a list. -->
        <section v-if="tab === 'profile'" class="panel">
          <p class="panel-desc">{{ t('memorySettings.profile.description') }}</p>

          <div v-if="!profile" class="empty">
            <p class="empty-title">{{ t('memorySettings.profile.emptyTitle') }}</p>
            <p class="empty-desc">{{ t('memorySettings.profile.emptyDescription') }}</p>
          </div>

          <template v-else>
            <div class="profile-meta">
              <span>{{ t('memorySettings.profile.revision', { revision: profile.revision }) }}</span>
              <span>{{ t('memorySettings.profile.builtFrom', { count: profile.episode_count }) }}</span>
              <span v-if="profile.updated_at">
                {{ t('memorySettings.profile.updatedAt', { time: formatTime(profile.updated_at) }) }}
              </span>
            </div>
            <!-- A rewrite replaces the whole body, so a person who typed their own
                 wording needs to know it is the thing at stake. -->
            <p v-if="profile.user_edited_at" class="panel-hint">
              {{ t('memorySettings.profile.userEdited') }}
            </p>
            <t-textarea
              v-model="profileDraft"
              class="profile-editor"
              :disabled="!canWrite"
              :maxlength="MEMORY_PROFILE_MAX_LENGTH"
              :autosize="{ minRows: 12, maxRows: 26 }"
              :placeholder="t('memorySettings.profile.placeholder')"
            />
            <div class="profile-footer">
              <span class="profile-length">
                {{ t('memorySettings.profile.length', {
                  count: profileDraft.length,
                  max: MEMORY_PROFILE_MAX_LENGTH,
                }) }}
              </span>
              <div class="profile-actions">
                <t-popconfirm
                  theme="danger"
                  :content="t('memorySettings.profile.deleteConfirm')"
                  :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                  :cancel-btn="t('common.cancel')"
                  placement="top"
                  @confirm="handleDeleteProfile"
                >
                  <t-button size="small" theme="danger" variant="text" :disabled="!canWrite">
                    {{ t('memorySettings.profile.delete') }}
                  </t-button>
                </t-popconfirm>
                <t-button
                  size="small"
                  theme="primary"
                  :loading="savingProfile"
                  :disabled="!canWrite || !profileDirty"
                  @click="handleSaveProfile"
                >
                  {{ t('common.save') }}
                </t-button>
              </div>
            </div>
          </template>
        </section>

        <!-- Episodes: a browsable history. Summaries run to paragraphs, so a row
             stays one line until it is opened. -->
        <section v-else-if="tab === 'episodes'" class="panel">
          <p class="panel-desc">{{ t('memorySettings.episodes.description') }}</p>

          <div v-if="episodes.length === 0" class="empty">
            <p class="empty-title">{{ t('memorySettings.episodes.emptyTitle') }}</p>
            <p class="empty-desc">{{ t('memorySettings.episodes.emptyDescription') }}</p>
          </div>

          <ul v-else class="memory-list">
            <li v-for="episode in episodes" :key="episode.id" class="memory-item">
              <div class="memory-main">
                <button
                  type="button"
                  class="episode-head"
                  :aria-expanded="expandedId === episode.id"
                  @click="toggleEpisode(episode)"
                >
                  <t-icon
                    class="episode-chevron"
                    :name="expandedId === episode.id ? 'chevron-down' : 'chevron-right'"
                    size="14px"
                  />
                  <span class="episode-title">{{ episode.title }}</span>
                </button>
                <div class="memory-meta">
                  <span>{{ formatRange(episode.from_at, episode.to_at) }}</span>
                  <span :class="outcomeClass(episode.outcome)">{{ outcomeLabel(episode.outcome) }}</span>
                  <!-- Recall ranks on use, so this is the answer to "why is this
                       one still here" and belongs on every row. -->
                  <span>{{ t('memorySettings.episodes.useCount', { count: episode.use_count }) }}</span>
                </div>
                <div v-if="episode.keywords?.length" class="episode-keywords">
                  <span v-for="keyword in episode.keywords" :key="keyword" class="episode-keyword">
                    {{ keyword }}
                  </span>
                </div>
                <p v-if="expandedId === episode.id" class="episode-summary">
                  {{ expandedSummary || t('memorySettings.episodes.summaryEmpty') }}
                </p>
              </div>
              <div class="memory-actions">
                <t-popconfirm
                  theme="danger"
                  :content="t('memorySettings.episodes.deleteConfirm')"
                  :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                  :cancel-btn="t('common.cancel')"
                  placement="left"
                  @confirm="handleDeleteEpisode(episode)"
                >
                  <t-button
                    size="small"
                    theme="danger"
                    variant="text"
                    shape="square"
                    :title="t('common.delete')"
                  >
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-popconfirm>
              </div>
            </li>
          </ul>

          <t-pagination
            v-if="episodeTotal > pageSize"
            class="memory-pagination"
            :total="episodeTotal"
            :page-size="pageSize"
            :current="page"
            :show-jumper="false"
            :show-page-size="false"
            @current-change="handlePageChange"
          />
        </section>

        <!-- Notes: the user's own sentences, kept word for word. -->
        <section v-else class="panel">
          <p class="panel-desc">{{ t('memorySettings.notes.description') }}</p>

          <div class="note-add">
            <t-textarea
              v-model="noteDraft"
              :disabled="!canWrite || notesFull"
              :maxlength="MEMORY_NOTE_MAX_LENGTH"
              :autosize="{ minRows: 2, maxRows: 5 }"
              :placeholder="t('memorySettings.notes.placeholder')"
            />
            <div class="note-add-footer">
              <span class="note-count">
                {{ t('memorySettings.notes.count', {
                  count: notes.length,
                  max: MEMORY_NOTE_MAX_COUNT,
                }) }}
              </span>
              <t-button
                size="small"
                theme="primary"
                :loading="addingNote"
                :disabled="!canWrite || notesFull || !noteDraft.trim()"
                @click="handleAddNote"
              >
                {{ t('memorySettings.notes.add') }}
              </t-button>
            </div>
            <!-- Adding past the cap fails server-side; saying so up front beats a
                 toast that arrives after the sentence was typed. -->
            <p v-if="notesFull" class="panel-hint">
              {{ t('memorySettings.notes.full', { max: MEMORY_NOTE_MAX_COUNT }) }}
            </p>
          </div>

          <div v-if="notes.length === 0" class="empty">
            <p class="empty-title">{{ t('memorySettings.notes.emptyTitle') }}</p>
            <p class="empty-desc">{{ t('memorySettings.notes.emptyDescription') }}</p>
          </div>

          <ul v-else class="memory-list">
            <li v-for="note in notes" :key="note.id" class="memory-item">
              <div class="memory-main">
                <p class="memory-content">{{ note.content }}</p>
                <div class="memory-meta">
                  <span>{{ formatTime(note.created_at) }}</span>
                </div>
              </div>
              <div class="memory-actions">
                <t-popconfirm
                  theme="danger"
                  :content="t('memorySettings.notes.deleteConfirm')"
                  :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                  :cancel-btn="t('common.cancel')"
                  placement="left"
                  @confirm="handleDeleteNote(note)"
                >
                  <t-button
                    size="small"
                    theme="danger"
                    variant="text"
                    shape="square"
                    :title="t('common.delete')"
                  >
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-popconfirm>
              </div>
            </li>
          </ul>
        </section>
      </t-loading>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  clearAllMemories,
  consolidateMemory,
  createMemoryNote,
  deleteMemoryEpisode,
  deleteMemoryNote,
  deleteMemoryProfile,
  exportMemories,
  getMemoryEpisode,
  getMemoryProfile,
  getMemorySettings,
  listMemoryEpisodes,
  listMemoryNotes,
  updateMemoryEnabled,
  updateMemoryProfile,
  MEMORY_NOTE_MAX_COUNT,
  MEMORY_NOTE_MAX_LENGTH,
  MEMORY_PROFILE_MAX_LENGTH,
  type MemoryDigest,
  type MemoryEpisode,
  type MemoryEpisodeOutcome,
  type MemoryNote,
  type MemorySettings,
} from '@/api/memory'

const { t } = useI18n()

type MemoryTab = 'profile' | 'episodes' | 'notes'

const settings = ref<MemorySettings | null>(null)
const userEnabled = ref(false)
const tab = ref<MemoryTab>('profile')
const loading = ref(false)
const rewriting = ref(false)
const page = ref(1)
const pageSize = 20

const profile = ref<MemoryDigest | null>(null)
const profileLoaded = ref('')
const profileDraft = ref('')
const savingProfile = ref(false)

const episodes = ref<MemoryEpisode[]>([])
const episodeTotal = ref(0)
const expandedId = ref('')
const expandedSummary = ref('')

const notes = ref<MemoryNote[]>([])
const noteDraft = ref('')
const addingNote = ref(false)

const tabs: MemoryTab[] = ['profile', 'episodes', 'notes']
const usageRowKeys = ['profile', 'episodes', 'notes'] as const

const tabIcons: Record<MemoryTab, string> = {
  profile: 'user',
  episodes: 'chat',
  notes: 'bookmark',
}

const tabIcon = (value: MemoryTab) => tabIcons[value]

const tabLabel = (value: MemoryTab) => {
  const label = t(`memorySettings.tabs.${value}`)
  if (value === 'profile') return label
  if (value === 'episodes') return `${label}(${episodeTotal.value})`
  return `${label}(${notes.value.length})`
}

// Writing requires both switches; everything stays readable either way so a user
// who just turned memory off can still review and delete what is stored.
const canWrite = computed(() => settings.value?.effective === true)

const isEmptyStore = computed(
  () => !profile.value && episodeTotal.value === 0 && notes.value.length === 0,
)

const profileDirty = computed(() => profileDraft.value !== profileLoaded.value)

const notesFull = computed(() => notes.value.length >= MEMORY_NOTE_MAX_COUNT)

const outcomeLabel = (outcome: MemoryEpisodeOutcome) =>
  t(`memorySettings.episodes.outcomes.${outcome}`)

const outcomeClass = (outcome: MemoryEpisodeOutcome) => `episode-outcome outcome-${outcome}`

const formatTime = (value: string | null) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const now = new Date()
  const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (date.toDateString() === now.toDateString()) {
    return time
  }
  if (date.getFullYear() === now.getFullYear()) {
    return `${date.getMonth() + 1}/${date.getDate()} ${time}`
  }
  return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()}`
}

const formatRange = (from: string, to: string) => {
  const start = formatTime(from)
  const end = formatTime(to)
  if (!start) return end
  if (!end || start === end) return start
  return `${start} – ${end}`
}

const loadSettings = async () => {
  try {
    const response = await getMemorySettings()
    settings.value = response.data
    userEnabled.value = response.data.user_enabled
  } catch (error: any) {
    console.error('Failed to load memory settings:', error)
  }
}

const loadProfile = async () => {
  try {
    const response = await getMemoryProfile()
    profile.value = response.data || null
    profileLoaded.value = response.data?.body || ''
    profileDraft.value = profileLoaded.value
  } catch (error: any) {
    console.error('Failed to load memory profile:', error)
    profile.value = null
    profileLoaded.value = ''
    profileDraft.value = ''
  }
}

const loadEpisodes = async () => {
  try {
    const response = await listMemoryEpisodes({
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    episodes.value = response.data || []
    episodeTotal.value = response.total || 0
  } catch (error: any) {
    console.error('Failed to load memory episodes:', error)
    episodes.value = []
    episodeTotal.value = 0
  }
}

const loadNotes = async () => {
  try {
    const response = await listMemoryNotes({ limit: MEMORY_NOTE_MAX_COUNT })
    notes.value = response.data || []
  } catch (error: any) {
    console.error('Failed to load memory notes:', error)
    notes.value = []
  }
}

// The toolbar acts on all three stores at once and the tab labels carry counts,
// so every section is loaded even when only one of them is on screen.
const loadAll = async () => {
  loading.value = true
  try {
    await Promise.all([loadProfile(), loadEpisodes(), loadNotes()])
  } finally {
    loading.value = false
  }
}

const handleTabChange = (value: string | number) => {
  tab.value = value as MemoryTab
}

const handlePageChange = async (current: number) => {
  page.value = current
  expandedId.value = ''
  expandedSummary.value = ''
  await loadEpisodes()
}

const handleEnabledChange = async (value: boolean) => {
  try {
    const response = await updateMemoryEnabled(value)
    settings.value = response.data
    userEnabled.value = response.data.user_enabled
    MessagePlugin.success(
      value ? t('memorySettings.toasts.enabled') : t('memorySettings.toasts.disabled'),
    )
  } catch (error: any) {
    userEnabled.value = !value
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const handleSaveProfile = async () => {
  if (savingProfile.value) return
  savingProfile.value = true
  try {
    await updateMemoryProfile(profileDraft.value)
    await loadProfile()
    MessagePlugin.success(t('memorySettings.profile.saved'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  } finally {
    savingProfile.value = false
  }
}

const handleDeleteProfile = async () => {
  try {
    await deleteMemoryProfile()
    await loadProfile()
    MessagePlugin.success(t('memorySettings.profile.deleted'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

// The list carries titles only, so the narrative is fetched the first time a row
// is opened rather than pulling every summary into the page.
const toggleEpisode = async (episode: MemoryEpisode) => {
  if (expandedId.value === episode.id) {
    expandedId.value = ''
    expandedSummary.value = ''
    return
  }
  expandedId.value = episode.id
  expandedSummary.value = episode.summary || ''
  if (expandedSummary.value) return
  try {
    const response = await getMemoryEpisode(episode.id)
    if (expandedId.value !== episode.id) return
    expandedSummary.value = response.data?.summary || ''
  } catch (error: any) {
    if (expandedId.value !== episode.id) return
    expandedSummary.value = t('memorySettings.episodes.summaryFailed')
  }
}

const handleDeleteEpisode = async (episode: MemoryEpisode) => {
  try {
    await deleteMemoryEpisode(episode.id)
    if (expandedId.value === episode.id) {
      expandedId.value = ''
      expandedSummary.value = ''
    }
    // The last row of the last page leaves the page empty, so step back one.
    if (episodes.value.length === 1 && page.value > 1) page.value -= 1
    await loadEpisodes()
    await loadSettings()
    MessagePlugin.success(t('memorySettings.episodes.deleted'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const handleAddNote = async () => {
  const content = noteDraft.value.trim()
  if (!content || addingNote.value) return
  addingNote.value = true
  try {
    await createMemoryNote(content)
    noteDraft.value = ''
    await loadNotes()
    MessagePlugin.success(t('memorySettings.notes.added'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  } finally {
    addingNote.value = false
  }
}

const handleDeleteNote = async (note: MemoryNote) => {
  try {
    await deleteMemoryNote(note.id)
    await loadNotes()
    MessagePlugin.success(t('memorySettings.notes.deleted'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

// A rewrite that changes nothing is a normal outcome, so each reason says which
// one it was instead of leaving the button looking broken.
const rewriteSkipMessage: Record<string, string> = {
  too_soon: 'memorySettings.rewriteTooSoon',
  too_few_items: 'memorySettings.rewriteTooFewEpisodes',
}

const handleRewrite = async () => {
  if (rewriting.value) return
  rewriting.value = true
  try {
    const response = await consolidateMemory()
    const result = response.data
    if (result?.skipped === 'model_unavailable') {
      MessagePlugin.warning(t('memorySettings.rewriteModelUnavailable'))
    } else if (result?.skipped) {
      MessagePlugin.info(
        t(rewriteSkipMessage[result.skipped] ?? 'memorySettings.rewriteNothing'),
      )
    } else {
      MessagePlugin.success(
        t('memorySettings.rewriteSuccess', { count: result?.reviewed || 0 }),
      )
    }
    await loadProfile()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('memorySettings.rewriteFailed'))
  } finally {
    rewriting.value = false
  }
}

const handleClear = async () => {
  try {
    const response = await clearAllMemories()
    page.value = 1
    expandedId.value = ''
    expandedSummary.value = ''
    await loadAll()
    await loadSettings()
    MessagePlugin.success(t('memorySettings.toasts.cleared', { count: response.removed || 0 }))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

const handleExport = async () => {
  try {
    const response = await exportMemories()
    const blob = new Blob([JSON.stringify(response.data || {}, null, 2)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'weknora-memories.json'
    link.click()
    URL.revokeObjectURL(url)
    if (response.truncated) MessagePlugin.info(t('memorySettings.exportTruncated'))
  } catch (error: any) {
    MessagePlugin.error(t('memorySettings.toasts.saveFailed', { message: error?.message || '' }))
  }
}

onMounted(async () => {
  await loadSettings()
  await loadAll()
})
</script>

<style lang="less" scoped>
.memory-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 8px 0 0;
    line-height: 1.5;
  }
}

.section-header-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.usage-trigger-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  line-height: 0;
  transition: background-color 0.2s ease, color 0.2s ease;

  :deep(.t-icon) {
    display: block;
  }

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

.notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  margin-bottom: 16px;
  border-radius: 8px;
  background: var(--td-warning-color-1);
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.list-section {
  margin-top: 28px;
}

.list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.list-title {
  display: flex;
  align-items: baseline;
  gap: 8px;

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }
}

.list-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.status-tabs {
  margin-top: 0;

  :deep(.t-tabs__header) {
    margin: 0;
    background: transparent;
  }

  :deep(.t-tabs__nav-item) {
    font-size: 13px;
  }

  .status-tab-label {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }

  /* Spacing must live in padding, not margin: TDesign sums item widths
     (excluding margin) to place the active underline. */
  :deep(.t-tabs__nav-item-wrapper) {
    padding: 0 12px;
    margin: 0;
  }

  :deep(.t-tabs__bar + .t-tabs__nav-item .t-tabs__nav-item-wrapper) {
    padding-left: 0;
  }

  :deep(.t-tabs__bar) {
    height: 2px;
  }

  :deep(.t-tabs__operations) {
    display: none;
  }

  :deep(.t-tabs__content) {
    display: none;
  }

  :deep(.t-tabs__nav-container) {
    padding: 0;
  }
}

.panel {
  padding-top: 16px;
}

.panel-desc {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.panel-hint {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);
}

.profile-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 10px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);

  > span:not(:last-child)::after {
    content: '·';
    margin: 0 6px;
  }
}

.profile-editor {
  :deep(textarea) {
    font-size: 14px;
    line-height: 1.7;
  }
}

.profile-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 12px;
}

.profile-length {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.profile-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.memory-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.memory-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.memory-main {
  flex: 1;
  min-width: 0;
}

.memory-content {
  margin: 0 0 4px;
  font-size: 14px;
  line-height: 1.6;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.memory-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);

  > span:not(:last-child)::after {
    content: '·';
    margin: 0 6px;
    color: var(--td-text-color-placeholder);
  }
}

.episode-head {
  display: flex;
  align-items: baseline;
  gap: 6px;
  width: 100%;
  margin: 0 0 4px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--td-text-color-primary);
  font-size: 14px;
  line-height: 1.6;
  text-align: left;
  cursor: pointer;

  &:hover .episode-title {
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 2px;
    border-radius: 4px;
  }
}

.episode-chevron {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.episode-title {
  min-width: 0;
  font-weight: 500;
  word-break: break-word;
}

.episode-outcome {
  &.outcome-success {
    color: var(--td-success-color);
  }

  &.outcome-partial {
    color: var(--td-warning-color);
  }

  &.outcome-fail {
    color: var(--td-error-color);
  }
}

.episode-keywords {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.episode-keyword {
  padding: 0 6px;
  border-radius: 3px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 18px;
}

.episode-summary {
  margin: 10px 0 0;
  padding-left: 20px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.memory-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  margin-top: -2px;
}

.memory-pagination {
  margin-top: 16px;
}

.note-add {
  margin-bottom: 8px;
}

.note-add-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 8px;
}

.note-count {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.empty {
  padding: 32px 0;
  text-align: center;
}

.empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px 0;
}

.empty-desc {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin: 0;
}
</style>

<!-- t-popup renders into body, so the usage popover has to be styled globally. -->
<style lang="less">
.memory-usage-popup-overlay {
  z-index: 3050 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 380px;
    max-width: calc(100vw - 24px);
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
  }

  .usage-popup {
    padding: 14px 16px 12px;
  }

  .usage-popup-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .usage-popup-intro {
    margin: 4px 0 12px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }

  .usage-popup-rows {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .usage-popup-row {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    line-height: 1.5;
  }

  .usage-popup-label {
    flex: 0 0 88px;
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .usage-popup-text {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }
}

:root[theme-mode='dark'] .memory-usage-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 4px rgba(0, 0, 0, 0.12),
    0 8px 32px rgba(0, 0, 0, 0.28) !important;
}
</style>
