<template>
  <t-dialog
    :visible="!!secret"
    :header="t('pluginAdmin.secret.title')"
    :confirm-btn="{ content: t('pluginAdmin.secret.copy'), theme: 'primary' }"
    :cancel-btn="{ content: t('pluginAdmin.secret.done') }"
    :close-on-overlay-click="false"
    @confirm="copyWithToast(secret, 'pluginAdmin.secret.copied')"
    @close="emit('update:secret', '')"
    @cancel="emit('update:secret', '')"
  >
    <p>{{ t('pluginAdmin.secret.description') }}</p>
    <t-textarea :value="secret" readonly autosize />
  </t-dialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { copyWithToast } from '@/utils/clipboard'

// A remote plugin's signing secret, shown once after it is issued: the
// service needs it to check requests, and WeKnora never shows it again.
defineProps<{ secret: string }>()
const emit = defineEmits<{ 'update:secret': [value: string] }>()
const { t } = useI18n()
</script>
