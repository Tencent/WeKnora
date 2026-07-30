<template>
    <div>
        <t-textarea resize="none" :autosize="false" v-model="value" :placeholder="$t('chat.enterDescription')" name="description" @change="onChange" />
    </div>
</template>
<script setup>
import { onMounted, watch, computed, ref, reactive } from 'vue';
import { useI18n } from 'vue-i18n';

const value = ref('');
const { t } = useI18n();
const onChange = (value,e) => {
    console.log(value)
}
</script>
<style lang="less">
.chat {
    width: 800px;
    font-size: 20px;
    margin: 0px auto
}

/* ============= Responsive Send Message ============= */
@media (max-width: 767px) {
  .send-message {
    padding: 8px !important;
  }

  .send-message .input-area {
    min-height: 40px !important;
  }

  .send-message .toolbar {
    gap: 4px !important;
  }

  .send-message .toolbar .tool-btn {
    width: 28px !important;
    height: 28px !important;
  }
}
</style>