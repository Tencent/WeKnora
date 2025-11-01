<template>
    <div class="chat">
        <div ref="scrollContainer" class="chat_scroll_box" @scroll="handleScroll">
            <div class="msg_list">
                <div v-for="(session, id) in messagesList" :key='id'>
                    <div v-if="session.role == 'user'">
                        <usermsg :content="session.content"></usermsg>
                    </div>
                    <div v-if="session.role == 'assistant'">
                        <botmsg :content="session.content" :session="session" @scroll-bottom="scrollToBottom"
                            :isFirstEnter="isFirstEnter"></botmsg>
                    </div>
                </div>
                <div v-if="loading"
                    style="height: 41px;display: flex;align-items: center;background: #fff;width: 58px;">
                    <img class="botanswer_laoding_gif" src="@/assets/img/botanswer_loading.gif" :alt="$t('chat.waitingForAnswer')">
                </div>
            </div>
        </div>
        <div style="min-height: 115px; margin: 16px auto 4px;width: 100%;max-width: 800px;">
            <InputField @send-msg="sendMsg" :isReplying="isReplying"></InputField>
        </div>
    </div>
</template>
<script setup>
import { storeToRefs } from 'pinia';
import { ref, onMounted, onUnmounted, nextTick, watch, reactive, onBeforeUnmount } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router';
import { useI18n } from 'vue-i18n';
import InputField from '../../components/Input-field.vue';
import botmsg from './components/botmsg.vue';
import usermsg from './components/usermsg.vue';
import { getMessageList, generateSessionsTitle } from "@/api/chat/index";
import { useStream } from '../../api/chat/streame'
import { useMenuStore } from '@/stores/menu';
const { t } = useI18n();
const usemenuStore = useMenuStore();
const { menuArr, isFirstSession, firstQuery } = storeToRefs(usemenuStore);
const { output, onChunk, isStreaming, isLoading, error, startStream, stopStream } = useStream();
const route = useRoute();
const router = useRouter();
const session_id = ref(route.params.chatid);
const knowledge_base_id = ref(route.params.kbId);
const created_at = ref('');
const limit = ref(20);
const messagesList = reactive([]);
const isReplying = ref(false);
const scrollLock = ref(false);
const isNeedTitle = ref(false);
const isFirstEnter = ref(true);
const loading = ref(false);
let fullContent = ref('')
let userquery = ref('')
const scrollContainer = ref(null)
watch([() => route.params], (newvalue) => {
    isFirstEnter.value = true;
    if (newvalue[0].chatid) {
        if (!firstQuery.value) {
            scrollLock.value = false;
        }
        messagesList.splice(0);
        session_id.value = newvalue[0].chatid;
        knowledge_base_id.value = newvalue[0].kbId;
        checkmenuTitle(session_id.value)
        let data = {
            session_id: session_id.value,
            created_at: '',
            limit: limit.value
        }
        getmsgList(data);
    }
});
const scrollToBottom = () => {
    nextTick(() => {
        if (scrollContainer.value) {
            scrollContainer.value.scrollTop = scrollContainer.value.scrollHeight;
        }
    })
}
const debounce = (fn, delay) => {
    let timer
    return (...args) => {
        clearTimeout(timer)
        timer = setTimeout(() => fn(...args), delay)
    }
}
const onChatScrollTop = () => {
    if (scrollLock.value) return;
    const { scrollTop, scrollHeight } = scrollContainer.value;
    isFirstEnter.value = false
    if (scrollTop == 0) {
        let data = {
            session_id: session_id.value,
            created_at: created_at.value,
            limit: limit.value
        }
        getmsgList(data, true, scrollHeight);
    }
}
const handleScroll = debounce(onChatScrollTop, 500);

const getmsgList = (data, isScrollType = false, scrollHeight) => {
    getMessageList(data).then(res => {
        if (res && res.data?.length) {
            created_at.value = res.data[0].created_at;
            handleMsgList(res.data, isScrollType, scrollHeight);
        }
    })
}
const handleMsgList = async (data, isScrollType = false, newScrollHeight) => {
    let chatlist = data.reverse()
    for (let i = 0, len = chatlist.length; i < len; i++) {
        let item = chatlist[i];
        item.thinking = false;
        if (item.content) {
            if (!item.content.includes('<think>') && !item.content.includes('<\/think>')) {
                item.thinkContent = "";
                item.content = item.content;
                item.showThink = false;
            } else if (item.content.includes('<\/think>')) {
                const arr = item.content.trim().split('<\/think>');
                item.showThink = true;
                item.thinkContent = arr[0].trim().replace('<think>', '');
                let index = item.content.trim().lastIndexOf('<\/think>')
                item.content = item.content.substring(index + 8);
            }
        }
        if (item.is_completed && !item.content) {
            item.content = t('chat.cannotAnswer');
        }
        messagesList.unshift(item);
        if (isFirstEnter.value) {
            scrollToBottom();
        } else if (isScrollType) {
            nextTick(() => {
                const { scrollHeight } = scrollContainer.value;
                scrollContainer.value.scrollTop = scrollHeight - newScrollHeight
            })
        }
    }
    if (messagesList[messagesList.length - 1] && !messagesList[messagesList.length - 1].is_completed) {
        isReplying.value = true;
        await startStream({ session_id: session_id.value, query: messagesList[messagesList.length - 1].id, method: 'GET', url: '/api/v1/sessions/continue-stream' });
    }

}
const checkmenuTitle = (session_id) => {
    menuArr.value[1].children?.forEach(item => {
        if (item.id == session_id) {
            isNeedTitle.value = item.isNoTitle;
        }
    });
}
// 发送消息
const sendMsg = async (value) => {
    userquery.value = value;
    isReplying.value = true;
    loading.value = true;
    messagesList.push({ content: value, role: 'user' });
    scrollToBottom();
    
    await startStream({ 
        session_id: session_id.value, 
        knowledge_base_id: knowledge_base_id.value,
        query: value, 
        method: 'POST', 
        url: '/api/v1/knowledge-chat' 
    });
}

// 处理流式数据
onChunk((data) => {
    loading.value = false;
    fullContent.value += data.content;
    let obj = { ...data, content: '', role: 'assistant', showThink: false };

    if (fullContent.value.includes('<think>') && !fullContent.value.includes('<\/think>')) {
        obj.thinking = true;
        obj.showThink = true;
        obj.content = '';
        obj.thinkContent = fullContent.value.replace('<think>', '').trim();
    } else if (fullContent.value.includes('<think>') && fullContent.value.includes('<\/think>')) {
        obj.thinking = false;
        obj.showThink = true;
        const index = fullContent.value.indexOf('<\/think>');
        obj.thinkContent = fullContent.value.substring(0, index).replace('<think>', '').trim();
        obj.content = fullContent.value.substring(index + 8).trim();
    } else {
        obj.content = fullContent.value;
    }
    if (data.done) {
        if (isFirstSession.value || isNeedTitle.value) {
            generateSessionsTitle(session_id.value, {
                messages: [{ role: "user", content: userquery.value }]
            }).then(res => {
                if (res.data) {
                    usemenuStore.changeIsFirstSession(false);
                    usemenuStore.updatasessionTitle(session_id.value, res.data);
                    isNeedTitle.value = false;
                }
            })
        }
        isReplying.value = false;
        fullContent.value = "";
    }
    updateAssistantSession(obj);
})
const updateAssistantSession = (payload) => {
    const message = messagesList.findLast((item) => {
        if (item.request_id === payload.id) {
            return true
        }
        return item.id === payload.id;
    });
    if (message) {
        message.content = payload.content;
        message.thinking = payload.thinking;
        message.thinkContent = payload.thinkContent;
        message.showThink = payload.showThink;
        message.knowledge_references = message.knowledge_references ? message.knowledge_references : payload.knowledge_references;
    } else {
        messagesList.push(payload);
    }
    scrollToBottom();
}
onMounted(() => {
    messagesList.splice(0);
    // scrollContainer.value.addEventListener("scroll", () => {
    //     if (scrollContainer.value.scrollTop == 0) {
    //         onChatScrollTop();
    //     }
    // });
    checkmenuTitle(session_id.value)
    if (firstQuery.value) {
        scrollLock.value = true;
        sendMsg(firstQuery.value);
        usemenuStore.changeFirstQuery('');
    } else {
        scrollLock.value = false;
        let data = {
            session_id: session_id.value,
            created_at: '',
            limit: limit.value
        }
        getmsgList(data)
    }
})
const clearData = () => {
    stopStream();
    isReplying.value = false;
    fullContent.value = '';
    userquery.value = '';

}
onBeforeRouteLeave((to, from, next) => {
    clearData()
    next()
})
onBeforeRouteUpdate((to, from, next) => {
    clearData()
    next()
})
</script>
<style lang="less" scoped>
.chat {
    font-size: 20px;
    padding: 20px;
    box-sizing: border-box;
    flex: 1;
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    max-width: calc(100vw - 260px);
    min-width: 400px;

    :deep(.answers-input) {
        position: static;
        transform: translateX(0);

        .t-textarea__inner {
            width: 100% !important;
        }
    }
}

.chat_scroll_box {
    flex: 1;
    width: 100%;
    overflow-y: auto;
    max-width: 800px;

    &::-webkit-scrollbar {
        width: 0;
        height: 0;
        color: transparent;
    }
}


.msg_list {
    display: flex;
    flex-direction: column;
    gap: 16px;
    max-width: 800px;
    flex: 1;

    .botanswer_laoding_gif {
        width: 24px;
        height: 18px;
        margin-left: 16px;
    }
}
</style>