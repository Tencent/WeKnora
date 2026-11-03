<template>
    <t-dialog
        v-model:visible="visible"
        :header="$t('agent.artifactDrawer.addToKnowledgeTitle')"
        :confirm-btn="{
            content: $t('common.confirm'),
            loading: submitting,
            disabled: !selectedKb || submitting,
        }"
        :cancel-btn="{ content: $t('common.cancel') }"
        width="480px"
        attach="body"
        @confirm="submit"
    >
        <div class="add-artifact-kb">
            <div class="add-artifact-kb__file">
                <t-icon :name="getFileIcon(fileName)" size="18px" />
                <span class="add-artifact-kb__name" :title="fileName">{{ fileName }}</span>
            </div>
            <t-select
                v-model="selectedKb"
                class="add-artifact-kb__select"
                :options="kbOptions"
                :loading="loading"
                :placeholder="$t('agent.artifactDrawer.addToKnowledgeSelect')"
                filterable
            />
            <p class="add-artifact-kb__hint">{{ $t('agent.artifactDrawer.addToKnowledgeHint') }}</p>
        </div>
    </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { addArtifactToKnowledgeBase } from '@/api/chat'
import { useChatResourcesStore } from '@/stores/chatResources'
import { getFileIcon } from '@/utils/files'

const props = defineProps<{
    visible: boolean
    sessionId: string
    messageId: string
    artifactIndex: number
    fileName: string
}>()

const emit = defineEmits<{
    (e: 'update:visible', v: boolean): void
    (e: 'added', payload: any): void
}>()

const { t } = useI18n()
const chatResources = useChatResourcesStore()
const selectedKb = ref('')
const loading = ref(false)
const submitting = ref(false)

const visible = computed({
    get: () => props.visible,
    set: (v: boolean) => emit('update:visible', v),
})

const kbOptions = computed(() =>
    (chatResources.rawKnowledgeBases as any[])
        .filter((kb) => kb && kb.id && kb.type !== 'faq')
        .map((kb) => ({ label: kb.name || kb.id, value: kb.id })),
)

const selectedKbName = computed(
    () => kbOptions.value.find((option) => option.value === selectedKb.value)?.label || '',
)

watch(
    () => props.visible,
    async (open) => {
        if (!open) return
        selectedKb.value = ''
        loading.value = true
        try {
            await chatResources.ensureKnowledgeBases()
        } catch {
            /* the select stays empty; the user sees the placeholder */
        } finally {
            loading.value = false
        }
    },
)

async function submit() {
    if (!selectedKb.value || submitting.value) return
    submitting.value = true
    try {
        const res: any = await addArtifactToKnowledgeBase(
            props.sessionId,
            props.messageId,
            props.artifactIndex,
            selectedKb.value,
        )
        const data = res?.data ?? res
        if (data?.duplicate) {
            MessagePlugin.info(t('agent.artifactDrawer.addToKnowledgeDuplicate'))
        } else {
            MessagePlugin.success(
                t('agent.artifactDrawer.addToKnowledgeSuccess', { name: selectedKbName.value }),
            )
        }
        emit('added', data)
        visible.value = false
    } catch (err: any) {
        MessagePlugin.error(
            err?.message || t('agent.artifactDrawer.addToKnowledgeFailed'),
        )
    } finally {
        submitting.value = false
    }
}
</script>

<style lang="less" scoped>
.add-artifact-kb {
    display: flex;
    flex-direction: column;
    gap: 12px;

    &__file {
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 8px 10px;
        border: 1px solid var(--td-component-stroke);
        border-radius: var(--td-radius-medium);
        background: var(--td-bg-color-container);
    }

    &__name {
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        color: var(--td-text-color-primary);
    }

    &__hint {
        margin: 0;
        font-size: var(--td-font-size-body-small);
        color: var(--td-text-color-placeholder);
    }
}
</style>
