<script setup lang="ts">
import { ref, defineEmits, onMounted, defineProps, defineExpose } from "vue";
import { useI18n } from 'vue-i18n';
import useKnowledgeBase from '@/hooks/useKnowledgeBase';
import { onBeforeRouteUpdate } from 'vue-router';
import { MessagePlugin } from "tdesign-vue-next";

const { t } = useI18n();
let { cardList, total, getKnowled } = useKnowledgeBase()
let query = ref("");
const props = defineProps({
  isReplying: {
    type: Boolean,
    required: false
  }
});
onMounted(() => {
  getKnowled()
})
const emit = defineEmits(['send-msg']);
const createSession = (val: string) => {
  if (!val.trim()) {
    MessagePlugin.info(t('chat.pleaseEnterContent'));
    return
  }
  if (!query.value && cardList.value.length == 0) {
    MessagePlugin.info(t('chat.pleaseUploadKnowledgeBase'));
    return;
  }
  if (props.isReplying) {
    return MessagePlugin.error(t('chat.replyingPleaseWait'));
  }
  emit('send-msg', val);
  clearvalue();
}
const clearvalue = () => {
  query.value = "";
}
const onKeydown = (val: string, event: { e: { preventDefault(): unknown; keyCode: number; shiftKey: any; ctrlKey: any; }; }) => {
  if ((event.e.keyCode == 13 && event.e.shiftKey) || (event.e.keyCode == 13 && event.e.ctrlKey)) {
    return;
  }
  if (event.e.keyCode == 13) {
    event.e.preventDefault();
    createSession(val)
  }
}
onBeforeRouteUpdate((to, from, next) => {
  clearvalue()
  next()
})

</script>
<template>
  <div class="answers-input">
    <t-textarea v-model="query" :placeholder="t('chat.askKnowledgeBase')" name="description" :autosize="true" @keydown="onKeydown" />
    <div class="answers-input-source">
      <span>{{ t('chat.sourcesCount', { count: total }) }}</span>
    </div>
    <div @click="createSession(query)" class="answers-input-send"
      :class="[query.length && total ? '' : 'grey-out']">
      <img src="../assets/img/sending-aircraft.svg" alt="">
    </div>
  </div>
</template>
<style scoped lang="less">
.answers-input {
  position: absolute;
  z-index: 99;
  bottom: 60px;
  left: 50%;
  transform: translateX(-400px);
}

:deep(.t-textarea__inner) {
  width: 100%;
  width: 800px;
  max-height: 250px !important;
  min-height: 112px !important;
  resize: none;
  color: #000000e6;
  font-size: 16px;
  font-weight: 400;
  line-height: 24px;
  color: #000000e6;
  font-family: "PingFang SC";
  padding: 16px 12px 12px 16px;
  border-radius: 12px;
  border: 1px solid #E7E7E7;
  box-sizing: border-box;
  background: #FFF;
  box-shadow: 0 6px 6px 0 #0000000a, 0 12px 12px -1px #00000014;

  &:focus {
    border: 1px solid #07C05F;
  }

  &:placeholder {
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 400;
    line-height: 24px;
  }
}

.answers-input-send {
  background-color: #07C05F;
  height: 36px;
  width: 36px;
  display: flex;
  justify-content: center;
  align-items: center;
  position: absolute;
  bottom: 12px;
  right: 12px;
  cursor: pointer;
  border-radius: 8px;
}

.answers-input-source {
  position: absolute;
  bottom: 12px;
  right: 64px;
  line-height: 32px;
  color: #00000066;
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 400;
}

.grey-out {
  background-color: #b5eccf !important;
  cursor: no-drop !important;
}


</style>
