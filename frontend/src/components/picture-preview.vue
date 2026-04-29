<script setup lang="ts">
import { watch } from "vue"

const props = defineProps(['reviewImg', 'reviewUrl'])
const emit = defineEmits(['closePreImg'])
const close = () => {
    emit('closePreImg')
}
</script>
<template>
    <t-image-viewer :visible="reviewImg" closeOnOverlay closeOnEscKeydown @close="close"
        :images="reviewUrl ? [reviewUrl] : []">
    </t-image-viewer>
</template>
<style scoped lang="less"></style>
