<template>
    <div class="dialogue-wrap">
        <div class="dialogue-answers">
            <div class="dialogue-title">
                <span>{{ t('chat.knowledgeBaseQandA') }}</span>
            </div>
            <InputField @send-msg="sendMsg"></InputField>
        </div>
    </div>
    

    <t-dialog v-model:visible="selectVisible" :header="t('knowledgeBase.title')" :confirmBtn="{ content: t('chat.newChat'), theme: 'primary' }" :onConfirm="confirmSelect" :onCancel="() => selectVisible = false">
        <t-form :data="{ kb: selectedKbId }">
            <t-form-item :label="t('knowledgeBase.title')">
                <t-select v-model="selectedKbId" :loading="kbLoading" :placeholder="t('knowledgeBase.selectKnowledgeBaseFirst')">
                    <t-option v-for="kb in kbList" :key="kb.id" :value="kb.id" :label="kb.name" />
                </t-select>
            </t-form-item>
        </t-form>
    </t-dialog>
</template>
<script setup lang="ts">
import { ref, onUnmounted, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import InputField from '@/components/Input-field.vue';
import EmptyKnowledge from '@/components/empty-knowledge.vue';
import { getSessionsList, createSessions, generateSessionsTitle } from "@/api/chat/index";
import { useMenuStore } from '@/stores/menu';
import { useRoute, useRouter } from 'vue-router';
import useKnowledgeBase from '@/hooks/useKnowledgeBase';
import { listKnowledgeBases } from '@/api/knowledge-base';

const { t } = useI18n();

let { cardList } = useKnowledgeBase()
const router = useRouter();
const route = useRoute();
const usemenuStore = useMenuStore();
const sendMsg = (value: string) => {
    createNewSession(value);
}

const selectVisible = ref(false)
const selectedKbId = ref<string>('')
const kbList = ref<Array<{ id: string; name: string }>>([])
const kbLoading = ref(false)

const ensureKbId = async (): Promise<string | null> => {
    // 1) 优先使用当前路由上下文（如果来自某个知识库详情页）
    const routeKb = (route.params as any)?.kbId as string
    if (routeKb) return routeKb


    // 3) 弹窗选择知识库（从接口拉取）
    kbLoading.value = true
    try {
        const res: any = await listKnowledgeBases()
        kbList.value = res?.data || []
        if (kbList.value.length === 0) return null
        selectedKbId.value = kbList.value[0].id
        selectVisible.value = true
        return null
    } finally {
        kbLoading.value = false
    }
}

async function createNewSession(value: string) {
    let knowledgeBaseId = await ensureKbId()
    if (!knowledgeBaseId) {
        // 等待用户在弹窗中选择
        pendingValue.value = value
        return
    }

    createSessions({ knowledge_base_id: knowledgeBaseId }).then(async res => {
        if (res.data && res.data.id) {
            await getTitle(res.data.id, value)
        } else {
            // 错误处理
            console.error(t('chat.createSessionFailed'));
        }
    }).catch(error => {
        console.error(t('chat.createSessionError') + ':', error);
    })
}

const pendingValue = ref<string>('')
const confirmSelect = async () => {
    if (!selectedKbId.value) return
    const value = pendingValue.value
    pendingValue.value = ''
    selectVisible.value = false
    createSessions({ knowledge_base_id: selectedKbId.value }).then(async res => {
        if (res.data && res.data.id) {
            await getTitle(res.data.id, value, selectedKbId.value)
        } else {
            console.error(t('chat.createSessionFailed'))
        }
    }).catch((e:any) => console.error(t('chat.createSessionError') + ':', e))
}

const getTitle = async (session_id: string, value: string, kbId?: string) => {
    const finalKbId = kbId || await ensureKbId();
    if (!finalKbId) {
        console.error(t('chat.unableToGetKnowledgeBaseId'));
        return;
    }
    
    let obj = { title: t('menu.newSession'), path: `chat/${finalKbId}/${session_id}`, id: session_id, isMore: false, isNoTitle: true }
    usemenuStore.updataMenuChildren(obj);
    usemenuStore.changeIsFirstSession(true);
    usemenuStore.changeFirstQuery(value);
    router.push(`/platform/chat/${finalKbId}/${session_id}`);
}

</script>
<style lang="less" scoped>
.dialogue-wrap {
    flex: 1;
    display: flex;
    justify-content: center;
    align-items: center;
    // position: relative;
}

.dialogue-answers {
    position: absolute;
    display: flex;
    flex-flow: column;
    align-items: center;

    :deep(.answers-input) {
        position: static;
        transform: translateX(0);
    }
}

.dialogue-title {
    display: flex;
    color: #000000;
    font-family: "PingFang SC";
    font-size: 28px;
    font-weight: 600;
    align-items: center;
    margin-bottom: 30px;

    .icon {
        display: flex;
        width: 32px;
        height: 32px;
        justify-content: center;
        align-items: center;
        border-radius: 6px;
        background: #FFF;
        box-shadow: 0 0 2px -1px #0000001f;
        margin-right: 12px;

        .logo_img {
            height: 24px;
            width: 24px;
        }
    }
}

@media (max-width: 1250px) and (min-width: 1045px) {
    .answers-input {
        transform: translateX(-329px);
    }

    :deep(.t-textarea__inner) {
        width: 654px !important;
    }
}

@media (max-width: 1045px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 500px !important;
    }
}
@media (max-width: 750px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 340px !important;
    }
}
@media (max-width: 600px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 300px !important;
    }
}

</style>
<style lang="less">
.del-menu-popup {
    z-index: 99 !important;

    .t-popup__content {
        width: 100px;
        height: 40px;
        line-height: 30px;
        padding-left: 14px;
        cursor: pointer;
        margin-top: 4px !important;

    }
}
</style>