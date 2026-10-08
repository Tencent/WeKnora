<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import { useData } from 'vitepress'

const { page } = useData()
const label = ref<HTMLElement>()

async function revealCurrentPage() {
  // Wait for VitePress to mark the current link and expand its section.
  await nextTick()
  const sidebar = label.value?.closest<HTMLElement>('.VPSidebar')
  const active = sidebar?.querySelector<HTMLElement>('.VPSidebarItem.is-active > .item > a')
  if (!sidebar || !active) return

  const bounds = sidebar.getBoundingClientRect()
  const item = active.getBoundingClientRect()
  const curtain = sidebar.querySelector<HTMLElement>('.curtain')?.getBoundingClientRect()
  const top = Math.max(bounds.top, curtain?.bottom ?? bounds.top) + 16
  const bottom = bounds.bottom - 16
  if (item.top >= top && item.bottom <= bottom) return

  // Move only the sidebar; scrollIntoView could also move the document body.
  sidebar.scrollTop += item.top - (top + (bottom - top - item.height) / 2)
}

onMounted(revealCurrentPage)
watch(() => page.value.relativePath, revealCurrentPage, { flush: 'post' })
</script>

<template>
  <p ref="label" class="wk-docs-label">使用文档</p>
</template>
