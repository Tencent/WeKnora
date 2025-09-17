<template>
    <div class="main" ref="dropzone" @dragover="dragover" @drop="drop" @dragstart="dragstart">
        <Menu></Menu>
        <RouterView />
        <div class="upload-mask" v-show="ismask">
            <input type="file" style="display: none" ref="uploadInput" accept=".pdf,.docx,.doc,.txt,.md" />
            <UploadMask></UploadMask>
        </div>
    </div>
</template>
<script setup lang="ts">
import Menu from '@/components/menu.vue'
import { ref } from 'vue';
import { useRouter } from 'vue-router'
import { storeToRefs } from "pinia";
import { knowledgeStore } from "@/stores/knowledge";
const usemenuStore = knowledgeStore();
import useKnowledgeBase from '@/hooks/useKnowledgeBase'
import UploadMask from '@/components/upload-mask.vue'
let { requestMethod } = useKnowledgeBase()
const router = useRouter();
let ismask = ref(false)
let dropzone = ref();
let uploadInput = ref();
const dragover = (event) => {
    event.preventDefault();
    ismask.value = true;
    if (((window.innerWidth - event.clientX) < 50) || ((window.innerHeight - event.clientY) < 50) || event.clientX < 50 || event.clientY < 50) {
        ismask.value = false
    }
}
const drop = (event) => {
    event.preventDefault();
    ismask.value = false
    const DataTransferItemList = event.dataTransfer.items;
    for (const dataTransferItem of DataTransferItemList) {
        const fileEntry = dataTransferItem.webkitGetAsEntry();
        if (fileEntry) {
            fileEntry.file((file: file) => {
                requestMethod(file, uploadInput)
                router.push('/platform/knowledge-bases?upload=true')
            })
        }
    }
}
const dragstart = (event) => {
    event.preventDefault();
}
</script>
<style lang="less">
.main {
    display: flex;
    width: 100%;
    height: 100%;
    min-width: 600px;
}

.upload-mask {
    background-color: rgba(255, 255, 255, 0.8);
    position: fixed;
    width: 100%;
    height: 100%;
    z-index: 999;
    display: flex;
    justify-content: center;
    align-items: center;
}

img {
    -webkit-user-drag: none;
    -khtml-user-drag: none;
    -moz-user-drag: none;
    -o-user-drag: none;
    user-drag: none;
}
</style>