<script setup lang="ts">
import { computed, nextTick, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ManualKnowledgeEditor from '@/components/manual-knowledge-editor.vue'
import { useAuthStore } from '@/stores/auth'

// TDesign locale configs
import enUSConfig from 'tdesign-vue-next/esm/locale/en_US'
import zhCNConfig from 'tdesign-vue-next/esm/locale/zh_CN'
import koKRConfig from 'tdesign-vue-next/esm/locale/ko_KR'
import ruRUConfig from 'tdesign-vue-next/esm/locale/ru_RU'

const { locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const tdLocaleMap: Record<string, object> = {
  'en-US': enUSConfig,
  'zh-CN': zhCNConfig,
  'ko-KR': koKRConfig,
  'ru-RU': ruRUConfig,
}

const tdGlobalConfig = computed(() => tdLocaleMap[locale.value] || enUSConfig)

const decodeOIDCResult = (encoded: string) => {
  const normalized = encoded.replace(/-/g, '+').replace(/_/g, '/')
  const padded = normalized + '='.repeat((4 - normalized.length % 4) % 4)
  const binary = window.atob(padded)
  const bytes = Uint8Array.from(binary, char => char.charCodeAt(0))
  return JSON.parse(new TextDecoder().decode(bytes))
}

const clearOIDCCallbackState = (path = '/') => {
  window.history.replaceState({}, document.title, path)
}

const persistOIDCLoginResponse = async (response: any) => {
  if (response.user && response.tenant && response.token) {
    authStore.setUser({
      id: response.user.id || '',
      username: response.user.username || '',
      email: response.user.email || '',
      avatar: response.user.avatar,
      tenant_id: String(response.tenant.id) || '',
      can_access_all_tenants: response.user.can_access_all_tenants || false,
      created_at: response.user.created_at || new Date().toISOString(),
      updated_at: response.user.updated_at || new Date().toISOString()
    })
    authStore.setToken(response.token)
    if (response.refresh_token) {
      authStore.setRefreshToken(response.refresh_token)
    }
    authStore.setTenant({
      id: String(response.tenant.id) || '',
      name: response.tenant.name || '',
      api_key: response.tenant.api_key || '',
      owner_id: response.user.id || '',
      created_at: response.tenant.created_at || new Date().toISOString(),
      updated_at: response.tenant.updated_at || new Date().toISOString()
    })
  }

  await nextTick()
  router.replace('/platform/knowledge-bases')
}

const handleGlobalOIDCCallback = async () => {
  const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : ''
  if (!hash) return

  const params = new URLSearchParams(hash)
  const oidcError = params.get('oidc_error')
  const oidcErrorDescription = params.get('oidc_error_description')
  const oidcResult = params.get('oidc_result')

  if (!oidcError && !oidcResult) return

  if (oidcError) {
    clearOIDCCallbackState('/login')
    await router.replace('/login')
    MessagePlugin.error(oidcErrorDescription || 'OIDC login failed')
    return
  }

  try {
    if (!oidcResult) {
      clearOIDCCallbackState('/login')
      await router.replace('/login')
      MessagePlugin.error('OIDC login failed')
      return
    }

    const response = decodeOIDCResult(oidcResult)
    if (response.success) {
      clearOIDCCallbackState('/')
      MessagePlugin.success('Login successful')
      await persistOIDCLoginResponse(response)
      return
    }

    clearOIDCCallbackState('/login')
    await router.replace('/login')
    MessagePlugin.error(response.message || 'OIDC login failed')
  } catch (error: any) {
    console.error('Global OIDC callback handling failed:', error)
    clearOIDCCallbackState('/login')
    await router.replace('/login')
    MessagePlugin.error(error.message || 'OIDC login failed')
  }
}

onMounted(() => {
  handleGlobalOIDCCallback()
})
</script>
<template>
  <t-config-provider :globalConfig="tdGlobalConfig">
    <div id="app">
      <RouterView />
      <ManualKnowledgeEditor />
    </div>
  </t-config-provider>
</template>
<style>
body,
html,
#app {
    width: 100%;
    height: 100%;
    margin: 0;
    padding: 0;
    font-size: 14px;
    font-family: Helvetica Neue, Helvetica, PingFang SC, Hiragino Sans GB,
        Microsoft YaHei, SimSun, sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    background: var(--td-bg-color-page);
    color: var(--td-text-color-primary);
}
</style>
