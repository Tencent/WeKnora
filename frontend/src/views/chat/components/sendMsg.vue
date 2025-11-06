<template>
    <div>
        <t-textarea resize="none" :autosize="false" v-model="value" :placeholder="$t('chat.enterDescription')" name="description" @change="onChange" />
    </div>
</template>
<script setup>
import { onMounted, watch, computed, ref, reactive } from 'vue';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
const value = ref('');
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
</style>