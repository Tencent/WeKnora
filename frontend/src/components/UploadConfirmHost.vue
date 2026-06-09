<script setup lang="ts">
import UploadConfirmDialog from '@/views/knowledge/components/UploadConfirmDialog.vue'
import { useUploadConfirmStore } from '@/stores/uploadConfirm'

const uploadConfirmStore = useUploadConfirmStore()

const handleConfirm = (payload: Parameters<typeof uploadConfirmStore.resolveConfirm>[0]) => {
  uploadConfirmStore.resolveConfirm(payload)
}

const handleCancel = () => {
  uploadConfirmStore.rejectConfirm()
}
</script>

<template>
  <UploadConfirmDialog
    v-model:visible="uploadConfirmStore.visible"
    :mode="uploadConfirmStore.mode"
    :kb-info="uploadConfirmStore.kbInfo"
    :files="uploadConfirmStore.files"
    :urls="uploadConfirmStore.urls"
    :manual-preview="uploadConfirmStore.manual"
    :reparse-preview="uploadConfirmStore.reparse"
    :accept-file-types="uploadConfirmStore.acceptFileTypes"
    :supported-file-types="uploadConfirmStore.supportedFileTypes"
    @confirm="handleConfirm"
    @cancel="handleCancel"
  />
</template>
