<template>
  <div class="cluster-table">
    <t-empty v-if="!clusters.length" description="暂无高频问题" />
    <t-table v-else row-key="id" :data="clusters" :columns="columns" hover :expanded-row-keys="expandedKeys" @expand-change="expandedKeys = $event">
      <template #representative_question="{ row }">
        <button class="cluster-table__question" type="button" @click="toggle(row.id)">{{ row.representative_question }}</button>
      </template>
      <template #expandedRow="{ row }">
        <div class="cluster-table__videos">
          <button v-for="video in row.videos" :key="video.video_id" type="button" :disabled="video.deleted" @click="!video.deleted && emit('selectVideoById', video.video_id, video.first_seconds)">
            <span>{{ video.deleted ? '视频已删除' : video.title }}</span><time>{{ video.first_timestamp }}</time>
          </button>
        </div>
      </template>
    </t-table>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { QuestionCluster } from '@/types/videohub'
defineProps<{ clusters: QuestionCluster[] }>()
const emit = defineEmits<{ selectVideoById: [videoId: string, seconds: number] }>()
const expandedKeys = ref<Array<string | number>>([])
const columns = [
  { colKey: 'representative_question', title: '代表性问题', ellipsis: true },
  { colKey: 'count', title: '提问次数', width: 120, sorter: (a: QuestionCluster, b: QuestionCluster) => a.count - b.count },
  { colKey: 'related_video_count', title: '关联视频数', width: 120 },
  { colKey: 'last_asked_at', title: '最近提问时间', width: 180, sorter: (a: QuestionCluster, b: QuestionCluster) => a.last_asked_at.localeCompare(b.last_asked_at) },
]
function toggle(id: string) { expandedKeys.value = expandedKeys.value.includes(id) ? expandedKeys.value.filter(key => key !== id) : [...expandedKeys.value, id] }
</script>

<style scoped>
.cluster-table { overflow: hidden; border: 1px solid var(--td-component-stroke); border-radius: var(--td-radius-extraLarge); }
.cluster-table__question { overflow: hidden; max-width: 100%; padding: 0; border: 0; background: transparent; color: var(--td-text-color-primary); font: inherit; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; cursor: pointer; }
.cluster-table__question:hover { color: var(--td-brand-color); }
.cluster-table__videos { display: grid; gap: calc(var(--td-comp-margin-s) / 2); padding: var(--td-comp-margin-s); }
.cluster-table__videos button { display: flex; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); padding: var(--td-comp-margin-s) calc(var(--td-comp-margin-s) * 1.5); border: 0; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary); cursor: pointer; }
.cluster-table__videos button:hover:not(:disabled) { color: var(--td-brand-color); }
.cluster-table__videos button:disabled { color: var(--td-text-color-disabled); cursor: not-allowed; }
.cluster-table__videos time { flex: none; color: var(--td-brand-color); font-family: var(--app-font-family-mono, monospace); }
</style>
