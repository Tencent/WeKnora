<template>
  <div class="kb-chunking-debug">
    <div class="debug-toggle" @click="open = !open">
      <span class="toggle-arrow" :class="{ open }">▸</span>
      <span class="toggle-label">{{ $t('knowledgeEditor.chunking.debug.toggle') }}</span>
      <span class="toggle-hint">{{ $t('knowledgeEditor.chunking.debug.toggleHint') }}</span>
    </div>

    <div v-if="open" class="debug-panel">
      <div class="debug-input">
        <label>{{ $t('knowledgeEditor.chunking.debug.sampleLabel') }}</label>
        <t-textarea
          v-model="sample"
          :placeholder="$t('knowledgeEditor.chunking.debug.samplePlaceholder')"
          :autosize="{ minRows: 6, maxRows: 14 }"
          :maxlength="MAX_CHARS"
        />
        <div class="input-meta">
          <span :class="{ warn: sample.length > MAX_CHARS * 0.8 }">
            {{ sample.length }} / {{ MAX_CHARS }} {{ $t('knowledgeEditor.chunking.characters') }}
          </span>
          <t-button
            theme="primary"
            :loading="loading"
            :disabled="!sample || sample.length === 0"
            @click="runPreview"
          >
            {{ $t('knowledgeEditor.chunking.debug.runButton') }}
          </t-button>
        </div>
      </div>

      <div v-if="error" class="debug-error">
        {{ error }}
      </div>

      <div v-if="result && !loading" class="debug-result">
        <!-- Tier summary -->
        <div class="result-header">
          <div class="tier-row">
            <span class="result-label">{{ $t('knowledgeEditor.chunking.debug.selectedTier') }}:</span>
            <t-tag
              :theme="tierTheme(result.selected_tier)"
              variant="light-outline"
              size="medium"
            >
              {{ tierDisplay(result.selected_tier) }}
            </t-tag>
            <span v-if="fallbackWarning" class="fallback-warning">
              {{ $t('knowledgeEditor.chunking.debug.fallbackWarning') }}
            </span>
          </div>
          <div v-if="result.rejected.length > 0" class="tier-row">
            <span class="result-label">{{ $t('knowledgeEditor.chunking.debug.rejected') }}:</span>
            <span class="rejection-list">
              <t-tag
                v-for="r in result.rejected"
                :key="r.tier"
                theme="default"
                variant="light"
                size="small"
              >
                {{ tierDisplay(r.tier) }}: {{ r.reason }}
              </t-tag>
            </span>
          </div>
        </div>

        <!-- Profile stats -->
        <div class="profile-grid">
          <div class="profile-cell">
            <div class="cell-value">{{ result.profile.total_lines }}</div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.lines') }}</div>
          </div>
          <div class="profile-cell">
            <div class="cell-value">{{ result.profile.total_chars }}</div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.chars') }}</div>
          </div>
          <div class="profile-cell">
            <div class="cell-value">{{ result.profile.md_heading_total }}</div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.headings') }}</div>
          </div>
          <div class="profile-cell">
            <div class="cell-value">{{ result.profile.form_feed_count }}</div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.pageBreaks') }}</div>
          </div>
          <div class="profile-cell">
            <div class="cell-value">
              {{
                result.profile.german_chapter_count +
                result.profile.english_chapter_count +
                result.profile.chinese_chapter_count
              }}
            </div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.chapterMarkers') }}</div>
          </div>
          <div class="profile-cell">
            <div class="cell-value">{{ (result.profile.detected_langs || []).join(', ') || '—' }}</div>
            <div class="cell-label">{{ $t('knowledgeEditor.chunking.debug.profile.languages') }}</div>
          </div>
        </div>

        <!-- Chunk stats line -->
        <div class="chunk-stats">
          <strong>{{ result.stats.count }}</strong>
          {{ $t('knowledgeEditor.chunking.debug.stats.chunks') }} —
          Ø {{ result.stats.avg_chars }} {{ $t('knowledgeEditor.chunking.characters') }},
          σ {{ result.stats.stddev_chars }},
          min {{ result.stats.min_chars }}, max {{ result.stats.max_chars }}
          <span v-if="result.stats.truncated_to" class="truncation-hint">
            ({{ $t('knowledgeEditor.chunking.debug.stats.truncated', { total: result.stats.truncated_to }) }})
          </span>
        </div>

        <!-- Chunks list -->
        <div class="chunks-list">
          <div
            v-for="c in result.chunks"
            :key="c.seq"
            class="chunk-card"
            :class="{ expanded: expandedChunks.has(c.seq) }"
          >
            <div class="chunk-meta" @click="toggleChunk(c.seq)">
              <span class="chunk-seq">#{{ c.seq }}</span>
              <span class="chunk-size">{{ c.size_chars }} {{ $t('knowledgeEditor.chunking.characters') }} / ~{{ c.size_tokens_approx }} tok</span>
              <span class="chunk-pos">[{{ c.start }}–{{ c.end }}]</span>
              <span class="chunk-toggle">{{ expandedChunks.has(c.seq) ? '−' : '+' }}</span>
            </div>
            <div v-if="c.context_header" class="chunk-header">
              <strong>{{ $t('knowledgeEditor.chunking.debug.contextHeader') }}:</strong>
              <pre>{{ c.context_header }}</pre>
            </div>
            <div class="chunk-content" :class="{ truncated: !expandedChunks.has(c.seq) }">
              <pre>{{ expandedChunks.has(c.seq) ? c.content : c.content.slice(0, 200) }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { previewChunking } from '@/api/chunker'
import type { PreviewChunkingResponse, StrategyTier } from '@/types/chunker'

interface Props {
  config: {
    chunkSize: number
    chunkOverlap: number
    separators: string[]
    strategy?: string
    tokenLimit?: number
    languages?: string[]
  }
}

const props = defineProps<Props>()
const { t } = useI18n()

const MAX_CHARS = 256 * 1024

const open = ref(false)
const sample = ref('')
const loading = ref(false)
const error = ref('')
const result = ref<PreviewChunkingResponse | null>(null)
const expandedChunks = ref(new Set<number>())

const fallbackWarning = computed(() => {
  if (!result.value) return false
  return result.value.selected_tier === 'legacy' && result.value.rejected.length > 0
})

const runPreview = async () => {
  loading.value = true
  error.value = ''
  result.value = null
  expandedChunks.value = new Set()
  try {
    const resp = await previewChunking({
      text: sample.value,
      chunking_config: {
        chunk_size: props.config.chunkSize,
        chunk_overlap: props.config.chunkOverlap,
        separators: props.config.separators,
        strategy: props.config.strategy || undefined,
        token_limit: props.config.tokenLimit || undefined,
        languages: props.config.languages?.length ? props.config.languages : undefined
      }
    })
    if (!resp.success) {
      throw new Error('preview failed')
    }
    result.value = resp.data
  } catch (e: any) {
    const msg = e?.message || e?.toString() || 'unknown error'
    error.value = t('knowledgeEditor.chunking.debug.errorPrefix') + ': ' + msg
    MessagePlugin.error(error.value)
  } finally {
    loading.value = false
  }
}

const toggleChunk = (seq: number) => {
  const next = new Set(expandedChunks.value)
  if (next.has(seq)) next.delete(seq)
  else next.add(seq)
  expandedChunks.value = next
}

const tierDisplay = (tier: StrategyTier) => {
  return t(`knowledgeEditor.chunking.strategies.${tier}.label`)
}

const tierTheme = (tier: StrategyTier) => {
  switch (tier) {
    case 'heading':
    case 'heuristic':
      return 'success'
    case 'recursive':
      return 'primary'
    case 'legacy':
    default:
      return 'default'
  }
}
</script>

<style lang="less" scoped>
.kb-chunking-debug {
  margin-top: 24px;
  border-top: 1px solid var(--td-component-stroke);
  padding-top: 16px;
}

.debug-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
  user-select: none;
  padding: 6px 0;

  &:hover {
    .toggle-label {
      color: var(--td-text-color-primary);
    }
  }
}

.toggle-arrow {
  display: inline-block;
  font-size: 12px;
  transition: transform 0.15s ease;
  color: var(--td-text-color-secondary);

  &.open {
    transform: rotate(90deg);
  }
}

.toggle-label {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.toggle-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.debug-panel {
  margin-top: 12px;
  padding: 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
}

.debug-input {
  label {
    display: block;
    font-size: 13px;
    font-weight: 500;
    margin-bottom: 6px;
    color: var(--td-text-color-primary);
  }

  .input-meta {
    margin-top: 8px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 12px;
    color: var(--td-text-color-secondary);

    .warn {
      color: var(--td-warning-color);
    }
  }
}

.debug-error {
  margin-top: 12px;
  padding: 10px 14px;
  background: var(--td-error-color-light);
  border-left: 3px solid var(--td-error-color);
  color: var(--td-error-color);
  font-size: 13px;
  border-radius: 0 4px 4px 0;
}

.debug-result {
  margin-top: 16px;
}

.result-header {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px dashed var(--td-component-stroke);
}

.tier-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
}

.result-label {
  color: var(--td-text-color-secondary);
  font-weight: 500;
  min-width: 120px;
}

.rejection-list {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.fallback-warning {
  color: var(--td-warning-color);
  font-size: 12px;
}

.profile-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
  padding: 12px;
  background: var(--td-bg-color-container-hover);
  border-radius: 4px;
}

.profile-cell {
  text-align: center;
}

.cell-value {
  font-size: 18px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.cell-label {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  margin-top: 2px;
}

.chunk-stats {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  margin-bottom: 12px;
  padding: 8px 12px;
  background: var(--td-bg-color-container-hover);
  border-radius: 4px;

  strong {
    color: var(--td-text-color-primary);
    font-size: 14px;
  }
}

.truncation-hint {
  color: var(--td-warning-color);
  font-size: 12px;
}

.chunks-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 500px;
  overflow-y: auto;
}

.chunk-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.chunk-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--td-bg-color-container-hover);
  cursor: pointer;
  font-size: 12px;
  color: var(--td-text-color-secondary);

  &:hover {
    background: var(--td-bg-color-component-hover);
  }
}

.chunk-seq {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.chunk-size {
  color: var(--td-text-color-primary);
}

.chunk-pos {
  color: var(--td-text-color-placeholder);
  font-family: monospace;
}

.chunk-toggle {
  margin-left: auto;
  font-weight: 700;
  font-size: 14px;
  color: var(--td-text-color-secondary);
}

.chunk-header {
  padding: 6px 12px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  font-size: 12px;

  strong {
    display: inline-block;
    margin-right: 8px;
    color: var(--td-text-color-secondary);
  }

  pre {
    display: inline;
    margin: 0;
    font-size: 12px;
    color: var(--td-brand-color);
    white-space: pre-wrap;
    word-break: break-word;
    font-family: var(--td-font-family-mono);
  }
}

.chunk-content {
  padding: 8px 12px;

  pre {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    color: var(--td-text-color-primary);
    font-family: var(--td-font-family-mono);
  }

  &.truncated pre::after {
    content: '…';
    color: var(--td-text-color-placeholder);
    margin-left: 4px;
  }
}
</style>
