<script setup lang="ts">
import { computed, h, ref, watch, onMounted, onUnmounted } from 'vue'
import { useData, useRoute } from 'vitepress'
import Search from 'vitepress/dist/client/theme-default/components/VPNavBarSearch.vue'
import { repositoryUrl, headerIcons, homeAssets } from '../../shared/header'
const { isDark } = useData()
const route = useRoute()
const docsNavigation = [
  { label: '快速开始', href: '/docs/01-getting-started/03-quickstart', section: '/docs/01-getting-started/' },
  { label: '架构', href: '/docs/02-architecture/01-overview', section: '/docs/02-architecture/' },
  { label: '功能', href: '/docs/03-features/01-tenant-auth', section: '/docs/03-features/' },
  { label: 'API', href: '/docs/04-api/01-api-overview', section: '/docs/04-api/' },
  { label: '客户端', href: '/docs/05-clients/01-frontend', section: '/docs/05-clients/' },
  { label: '开发', href: '/docs/06-development/01-dev-guide', section: '/docs/06-development/' },
  { label: '版本', href: '/docs/07-releases/v0.8.2', section: '/docs/07-releases/' },
]
const open = ref(false)
watch(() => route.path, () => open.value = false)
const menu = ref<HTMLButtonElement>()
const label = computed(() => isDark.value ? '切换到浅色' : '切换到深色')
const HeaderIcon = (props: { name: string }) => h('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.4, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' }, [h('path', { d: headerIcons[props.name] })])
function closeMenu(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) { open.value = false; menu.value?.focus() }
}
onMounted(() => document.addEventListener('keydown', closeMenu))
onUnmounted(() => document.removeEventListener('keydown', closeMenu))
</script>

<template>
  <header class="wk-header wk-docs-header">
    <div class="wk-header-inner">
      <a class="wk-brand" href="/" target="_self" aria-label="WeKnora 首页"><span class="wk-logo"><img :src="`${homeAssets}/brand/weknora-original.png`" alt="WeKnora" width="945" height="650"></span></a>
      <nav id="main-navigation" class="wk-navigation" :class="{ 'is-open': open }" aria-label="文档导航">
        <a v-for="item in docsNavigation" :key="item.href" :href="item.href" :aria-current="route.path.startsWith(item.section) ? 'page' : undefined" @click="open = false">{{ item.label }}</a>
        <div class="wk-header-search"><Search /></div>
        <a class="wk-mobile-github" :href="repositoryUrl" target="_blank" rel="noreferrer">GitHub <HeaderIcon name="external" /></a>
      </nav>
      <button class="wk-theme-toggle" type="button" role="switch" :aria-checked="isDark" :aria-label="label" :title="label" @click="isDark = !isDark"><HeaderIcon :name="isDark ? 'moon' : 'sun'" /></button>
      <a class="wk-header-github" :href="repositoryUrl" target="_blank" rel="noreferrer"><HeaderIcon name="github" /><span>GitHub</span></a>
      <button ref="menu" class="wk-menu-toggle" type="button" :aria-label="open ? '关闭导航' : '打开导航'" :aria-expanded="open" aria-controls="main-navigation" @click="open = !open"><HeaderIcon :name="open ? 'close' : 'menu'" /></button>
    </div>
  </header>
</template>
