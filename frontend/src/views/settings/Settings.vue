<template>
    <div class="settings-container">
        <div class="settings-header">
            <h2>{{ t('settings.systemConfig') }}</h2>
        </div>
        <div class="settings-form">
            <t-form ref="form" :data="formData" :rules="rules" @submit="onSubmit">
                <t-form-item :label="t('settings.apiEndpoint')" name="endpoint">
                    <t-input v-model="formData.endpoint" :placeholder="t('settings.enterApiEndpoint')" />
                </t-form-item>
                <t-form-item :label="t('settings.apiKey')" name="apiKey">
                    <t-input v-model="formData.apiKey" :placeholder="t('settings.enterApiKey')" />
                </t-form-item>
                <t-form-item :label="t('settings.knowledgeBaseId')" name="knowledgeBaseId">
                    <t-input v-model="formData.knowledgeBaseId" :placeholder="t('settings.enterKnowledgeBaseId')" />
                </t-form-item>
                <t-form-item>
                    <t-space>
                        <t-button theme="primary" type="submit">{{ t('settings.saveConfig') }}</t-button>
                        <t-button theme="default" @click="resetForm">{{ t('settings.reset') }}</t-button>
                    </t-space>
                </t-form-item>
            </t-form>
        </div>
    </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { useSettingsStore } from '@/stores/settings';

const { t } = useI18n();

const settingsStore = useSettingsStore();
const form = ref(null);

const formData = reactive({
    endpoint: '',
    apiKey: '',
    knowledgeBaseId: ''
});

const rules = computed(() => ({
    endpoint: [{ required: true, message: t('settings.enterApiEndpointRequired'), trigger: 'blur' }],
    apiKey: [{ required: true, message: t('settings.enterApiKeyRequired'), trigger: 'blur' }],
    knowledgeBaseId: [{ required: true, message: t('settings.enterKnowledgeBaseIdRequired'), trigger: 'blur' }]
}));

onMounted(() => {
    // 初始化表单数据
    const settings = settingsStore.getSettings();
    formData.endpoint = settings.endpoint;
    formData.apiKey = settings.apiKey;
    formData.knowledgeBaseId = settings.knowledgeBaseId;
});

const onSubmit = ({ validateResult }) => {
    if (validateResult === true) {
        settingsStore.saveSettings({
            endpoint: formData.endpoint,
            apiKey: formData.apiKey,
            knowledgeBaseId: formData.knowledgeBaseId
        });
        MessagePlugin.success(t('settings.configSaved'));
    }
};

const resetForm = () => {
    const settings = settingsStore.getSettings();
    formData.endpoint = settings.endpoint;
    formData.apiKey = settings.apiKey;
    formData.knowledgeBaseId = settings.knowledgeBaseId;
};
</script>

<style lang="less" scoped>
.settings-container {
    padding: 20px;
    background-color: #fff;
    border-radius: 8px;
    margin: 20px;
    min-height: 80vh;

    .settings-header {
        margin-bottom: 20px;
        border-bottom: 1px solid #f0f0f0;
        padding-bottom: 16px;

        h2 {
            font-size: 20px;
            font-weight: 600;
            color: #000000;
            margin: 0;
        }
    }

    .settings-form {
        max-width: 600px;
    }
}
</style> 