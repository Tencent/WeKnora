<script setup lang="ts">
import { computed } from 'vue'
import type { JournalRankMetadata } from '@/types/journalRank'
import { buildJournalRankBadges } from './journalRankBadges'

const props = withDefaults(defineProps<{
  rank?: JournalRankMetadata | null
  maxVisible?: number
  compact?: boolean
}>(), {
  rank: null,
  maxVisible: 5,
  compact: false,
})

const badges = computed(() => buildJournalRankBadges(props.rank))

const visibleBadges = computed(() => badges.value.slice(0, Math.max(1, props.maxVisible)))
const overflow = computed(() => Math.max(0, badges.value.length - visibleBadges.value.length))
const tooltip = computed(() => badges.value.map(item => item.label).join(' · '))
</script>

<template>
  <t-tooltip v-if="badges.length" :content="tooltip" placement="top">
    <div class="journal-rank-badges" :class="{ compact }" :aria-label="rank?.publication">
      <span
        v-for="badge in visibleBadges"
        :key="badge.key"
        class="journal-rank-badge"
        :class="`tone-${badge.tone}`"
      >{{ badge.label }}</span>
      <span v-if="overflow" class="journal-rank-overflow">+{{ overflow }}</span>
    </div>
  </t-tooltip>
</template>

<style scoped lang="less">
.journal-rank-badges {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
}

.journal-rank-badge,
.journal-rank-overflow {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  height: 20px;
  padding: 0 6px;
  border-radius: 4px;
  font-size: 11px;
  line-height: 20px;
  white-space: nowrap;
  letter-spacing: 0;
}

.compact .journal-rank-badge,
.compact .journal-rank-overflow {
  height: 18px;
  padding: 0 5px;
  line-height: 18px;
}

.tone-impact { color: #6d3b94; background: #f1e9f7; }
.tone-quartile { color: #b4382f; background: #fde9e6; }
.tone-cas { color: #347348; background: #e8f4ea; }
.tone-core { color: #c33b2d; background: #fff0ec; }
.tone-index { color: #4f5965; background: #eef0f2; }
.tone-warning { color: #a44316; background: #fff0df; }
.tone-custom { color: #315b80; background: #e7f0f8; }
.journal-rank-overflow { color: var(--td-text-color-secondary); background: var(--td-bg-color-secondarycontainer); }
</style>
