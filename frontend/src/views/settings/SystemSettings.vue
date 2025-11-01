<template>
    <div class="system-settings-container">
        <!-- 页面标题区域 -->
        <div class="settings-header">
            <h2>{{ isKbSettings ? t('settings.knowledgeBaseSettings') : t('settings.system') }}</h2>
            <p class="settings-subtitle">{{ isKbSettings ? t('settings.configureKbModels') : t('settings.manageSystemModels') }}</p>
        </div>
        
        <!-- 配置内容 -->
        <div class="settings-content">
            <!-- 系统设置：使用初始化配置 -->
            <InitializationContent v-if="!isKbSettings" />
            <!-- 知识库设置：基础信息与文档切分配置 -->
            <div v-else>
                <t-form :data="kbForm" @submit="saveKb">
                    <div class="config-section">
                        <h3><span class="section-icon">⚙️</span>{{ t('settings.basicInfo') }}</h3>
                        <t-form-item :label="t('settings.name')" name="name" :rules="[{ required: true, message: t('settings.enterNameRequired') }]">
                            <t-input v-model="kbForm.name" />
                        </t-form-item>
                        <t-form-item :label="t('settings.description')" name="description">
                            <t-textarea v-model="kbForm.description" />
                        </t-form-item>
                    </div>
                    <div class="config-section">
                        <h3><span class="section-icon">📄</span>{{ t('settings.documentSplitting') }}</h3>
                        <t-row :gutter="16">
                            <t-col :span="6">
                                <t-form-item :label="t('settings.chunkSize')" name="chunkSize">
                                    <t-input-number v-model="kbForm.config.chunking_config.chunk_size" :min="1" />
                                </t-form-item>
                            </t-col>
                            <t-col :span="6">
                                <t-form-item :label="t('settings.chunkOverlap')" name="chunkOverlap">
                                    <t-input-number v-model="kbForm.config.chunking_config.chunk_overlap" :min="0" />
                                </t-form-item>
                            </t-col>
                        </t-row>
                    </div>
                    <div class="submit-section">
                        <t-button theme="primary" type="submit" :loading="saving">{{ t('settings.save') }}</t-button>
                    </div>
                </t-form>
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { defineAsyncComponent, onMounted, reactive, ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getKnowledgeBaseById, updateKnowledgeBase } from '@/api/knowledge-base'

const { t } = useI18n()

// 异步加载初始化配置组件
const InitializationContent = defineAsyncComponent(() => import('../initialization/InitializationContent.vue'))

const route = useRoute()
const router = useRouter()
const isKbSettings = ref<boolean>(false)

interface KbForm {
    name: string
    description?: string
    config: { chunking_config: { chunk_size: number; chunk_overlap: number } }
}
const kbForm = reactive<KbForm>({
    name: '',
    description: '',
    config: { chunking_config: { chunk_size: 512, chunk_overlap: 64 } }
})
const saving = ref(false)

const loadKb = () => {
    const kbId = (route.params as any).kbId as string
    if (!kbId) return
    getKnowledgeBaseById(kbId).then((res: any) => {
        if (res?.data) {
            kbForm.name = res.data.name
            kbForm.description = res.data.description
            const cc = res.data.chunking_config || {}
            kbForm.config.chunking_config.chunk_size = cc.chunk_size ?? 512
            kbForm.config.chunking_config.chunk_overlap = cc.chunk_overlap ?? 64
        }
    })
}

onMounted(() => {
    isKbSettings.value = route.name === 'knowledgeBaseSettings'
    if (isKbSettings.value) loadKb()
})

const saveKb = () => {
    const kbId = (route.params as any).kbId as string
    if (!kbId) return
    saving.value = true
    updateKnowledgeBase(kbId, { name: kbForm.name, description: kbForm.description, config: { chunking_config: { chunk_size: kbForm.config.chunking_config.chunk_size, chunk_overlap: kbForm.config.chunking_config.chunk_overlap, separators: [], enable_multimodal: false }, image_processing_config: { model_id: '' } } })
    .then((res: any) => {
        if (res.success) {
            MessagePlugin.success(t('settings.saveSuccess'))
        } else {
            MessagePlugin.error(res.message || t('settings.saveFailed'))
        }
    })
    .catch((e: any) => MessagePlugin.error(e?.message || t('settings.saveFailed')))
    .finally(() => saving.value = false)
}
</script>

<style lang="less" scoped>
.system-settings-container {
    padding: 20px;
    background-color: #fff;
    margin: 0 20px 0 20px;
    height: calc(100vh);
    overflow-y: auto;
    box-sizing: border-box;
    flex: 1;
}

.settings-header {
    margin-bottom: 20px;
    border-bottom: 1px solid #f0f0f0;
    padding-bottom: 16px;

    h2 {
        font-size: 20px;
        font-weight: 600;
        color: #000000;
        margin: 0 0 8px 0;
    }

    .settings-subtitle {
        font-size: 14px;
        color: #666666;
        margin: 0;
    }
}

.settings-content {
    margin-top: 0;
}

/* 响应式设计 */
@media (max-width: 768px) {
    .system-settings-container {
        padding: 16px;
        margin: 10px;
        height: calc(100vh - 20px);
    }
    
    .settings-header h2 {
        font-size: 18px;
    }
}

/* 覆盖TDesign组件样式，与账户信息页面保持一致 */
:deep(.t-card) {
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
    border: 1px solid #e5e7eb;
}

/* 调整InitializationContent内部样式，使每个配置区域显示为独立卡片 */
:deep(.config-section) {
    background: #fff;
    border: 1px solid #e5e7eb;
    border-radius: 8px;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
    padding: 20px;
    margin-bottom: 20px;
    
    &:last-child {
        margin-bottom: 0;
    }
    
    h3 {
        font-size: 16px;
        font-weight: 600;
        color: #000000;
        margin: 0 0 16px 0;
        display: flex;
        align-items: center;
        padding: 0;
        background: none;
        border-left: none;
        border-radius: 0;
        border-bottom: 1px solid #f0f0f0;
        padding-bottom: 12px;
        
        .section-icon {
            margin-right: 8px;
            color: #07c05f;
            font-size: 18px;
        }
    }
}

:deep(.ollama-summary-card) {
    background: #fff;
    border: 1px solid #e5e7eb;
    border-radius: 8px;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
    padding: 20px;
    margin-bottom: 20px;
}

:deep(.submit-section) {
    margin-top: 20px;
    text-align: center;
}
</style>
