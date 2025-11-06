<template>
  <div class="kb-list-container">
    <div class="header">
      <h2>{{ t('knowledgeBase.title') }}</h2>
      <t-button theme="primary" @click="openCreate">{{ t('knowledgeBase.createNewKnowledgeBase') }}</t-button>
    </div>

    <!-- 未初始化知识库提示 -->
    <div v-if="hasUninitializedKbs" class="warning-banner">
      <t-icon name="info-circle" size="16px" />
      <span>{{ t('knowledgeBase.uninitializedWarning') }}</span>
    </div>
    <t-table :data="kbs" :columns="columns" row-key="id" size="medium" hover>
      <template #status="{ row }">
        <div class="status-cell">
          <t-tag
            :theme="isInitialized(row) ? 'success' : 'warning'"
            size="small"
          >
            {{ isInitialized(row) ? t('knowledgeBase.initializedStatus') : t('knowledgeBase.notInitializedStatus') }}
          </t-tag>
          <t-tooltip
            v-if="!isInitialized(row)"
            :content="t('knowledgeBase.needSettingsFirst')"
            placement="top"
          >
            <span class="warning-icon">⚠</span>
          </t-tooltip>
        </div>
      </template>
      <template #description="{ row }">
        <div class="description-text">{{ row.description || t('knowledgeBase.noDescription') }}</div>
      </template>
      <template #op="{ row }">
        <t-space size="small">
          <t-button 
            size="small" 
            @click="goDetail(row.id)"
            :disabled="!isInitialized(row)"
            :theme="isInitialized(row) ? 'primary' : 'default'"
            :variant="isInitialized(row) ? 'base' : 'outline'"
            :title="!isInitialized(row) ? t('knowledgeBase.configureModelsFirst') : ''"
          >
            {{ t('knowledgeBase.documents') }}
          </t-button>
          <t-button size="small" variant="outline" @click="goSettings(row.id)">{{ t('knowledgeBase.settings') }}</t-button>
          <t-popconfirm :content="t('knowledgeBase.confirmDeleteKnowledgeBase')" @confirm="remove(row.id)">
            <t-button size="small" theme="danger" variant="text">{{ t('knowledgeBase.delete') }}</t-button>
          </t-popconfirm>
        </t-space>
      </template>
    </t-table>

    <t-dialog v-model:visible="createVisible" :header="t('knowledgeBase.createKnowledgeBaseDialog')" :footer="false">
      <t-form :data="createForm" @submit="create">
        <t-form-item :label="t('knowledgeBase.name')" name="name" :rules="[{ required: true, message: t('knowledgeBase.enterNameKb') }]">
          <t-input v-model="createForm.name" />
        </t-form-item>
        <t-form-item :label="t('knowledgeBase.description')" name="description">
          <t-textarea v-model="createForm.description" />
        </t-form-item>
        <t-form-item>
          <t-space>
            <t-button theme="primary" type="submit" :loading="creating">{{ t('knowledgeBase.createKb') }}</t-button>
            <t-button variant="outline" @click="createVisible = false">{{ t('common.cancel') }}</t-button>
          </t-space>
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeBases, createKnowledgeBase, deleteKnowledgeBase } from '@/api/knowledge-base'
import { formatStringDate } from '@/utils/index'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

const router = useRouter()

interface KB { 
  id: string; 
  name: string; 
  description?: string; 
  updated_at?: string;
  embedding_model_id?: string;
  summary_model_id?: string;
}
const kbs = ref<KB[]>([])
const loading = ref(false)

const columns = [
  { colKey: 'name', title: t('knowledgeBase.name') },
  { colKey: 'description', title: t('knowledgeBase.description'), cell: 'description', width: 300 },
  { colKey: 'status', title: t('knowledgeBase.status'), cell: 'status', width: 100 },
  { colKey: 'updated_at', title: t('knowledgeBase.uploadTime') },
  { colKey: 'op', title: t('knowledgeBase.actions'), cell: 'op', width: 220 },
]

const fetchList = () => {
  loading.value = true
  listKnowledgeBases().then((res: any) => {
    const data = res.data || []
    // 格式化时间
    kbs.value = data.map((kb: KB) => ({
      ...kb,
      updated_at: kb.updated_at ? formatStringDate(new Date(kb.updated_at)) : ''
    }))
  }).finally(() => loading.value = false)
}

onMounted(fetchList)

const createVisible = ref(false)
const creating = ref(false)
const createForm = reactive({ name: '', description: '' })
const openCreate = () => {
  createForm.name = ''
  createForm.description = ''
  createVisible.value = true
}
const create = () => {
  if (!createForm.name) return
  creating.value = true
  const chunking_config = {
    chunk_size: 512,
    chunk_overlap: 100,
    separators: ['.', '?', '!', '。', '？', '！'],
    enable_multimodal: false
  }
  createKnowledgeBase({ name: createForm.name, description: createForm.description, chunking_config }).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(t('knowledgeBase.createSuccess'))
      createVisible.value = false
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('knowledgeBase.createFailed'))
    }
  }).catch((e: any) => {
    MessagePlugin.error(e?.message || t('knowledgeBase.createFailed'))
  }).finally(() => creating.value = false)
}

const remove = (id: string) => {
  deleteKnowledgeBase(id).then((res: any) => {
    if (res.success) {
      MessagePlugin.success(t('knowledgeBase.deleted'))
      fetchList()
    } else {
      MessagePlugin.error(res.message || t('knowledgeBase.deleteFailedKb'))
    }
  }).catch((e: any) => MessagePlugin.error(e?.message || t('knowledgeBase.deleteFailedKb')))
}

const isInitialized = (kb: KB) => {
  return !!(kb.embedding_model_id && kb.embedding_model_id !== '' && 
            kb.summary_model_id && kb.summary_model_id !== '')
}

// 计算是否有未初始化的知识库
const hasUninitializedKbs = computed(() => {
  return kbs.value.some(kb => !isInitialized(kb))
})

const goDetail = (id: string) => {
  router.push(`/platform/knowledge-bases/${id}`)
}
const goSettings = (id: string) => {
  router.push(`/platform/knowledge-bases/${id}/settings`)
}
</script>

<style scoped lang="less">
.kb-list-container {
  padding: 20px;
  background: #fff;
  margin: 0 20px 0 20px;
  height: calc(100vh);
  overflow-y: auto;
  box-sizing: border-box;
  flex: 1;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  h2 { margin: 0; font-size: 20px; font-weight: 600; }
}

.warning-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  margin-bottom: 16px;
  background: #fff7e6;
  border: 1px solid #ffd591;
  border-radius: 6px;
  color: #d46b08;
  font-size: 14px;
  
  .t-icon {
    color: #d46b08;
    flex-shrink: 0;
  }
}

.status-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  
  .warning-icon {
    color: #ff8800;
    cursor: pointer;
    font-size: 16px;
    font-weight: bold;
    transition: color 0.2s;
    
    &:hover {
      color: #d46b08;
    }
  }
}

.description-cell {
  .description-text {
    color: #000000e6;
    font-size: 14px;
  }
}
</style>
