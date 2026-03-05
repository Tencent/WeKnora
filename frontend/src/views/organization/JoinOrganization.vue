<template>
  <div class="join-page">
    <div class="join-card">
      <div class="join-icon">
        <t-icon name="user-add" size="48px" />
      </div>
      <h2 class="join-title">{{ $t('organization.join.title') }}</h2>
      <p v-if="loading" class="join-message">{{ $t('organization.join.joining') }}</p>
      <p v-else-if="error" class="join-message error">{{ error }}</p>
      <p v-else class="join-message success">{{ $t('organization.join.success') }}</p>
      
      <t-button 
        v-if="!loading" 
        theme="primary" 
        @click="goToOrganizations"
      >
        {{ $t('organization.join.goToOrganizations') }}
      </t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useOrganizationStore } from '@/stores/organization'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const orgStore = useOrganizationStore()

const loading = ref(true)
const error = ref('')

onMounted(async () => {
  const code = route.query.code as string
  
  if (!code) {
    error.value = t('organization.join.noCode')
    loading.value = false
    return
  }
  
  try {
    const result = await orgStore.join(code)
    if (result) {
      MessagePlugin.success(t('organization.join.success'))
    } else {
      error.value = orgStore.error || t('organization.join.failed')
    }
  } catch (e: any) {
    error.value = e?.message || t('organization.join.failed')
  } finally {
    loading.value = false
  }
})

const goToOrganizations = () => {
  router.push('/platform/organizations')
}
</script>

<style scoped lang="less">
.join-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--td-bg-color-container);
  padding: 20px;
}

.join-card {
  background: var(--td-bg-color-container);
  border-radius: 16px;
  padding: 48px;
  text-align: center;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.08);
  max-width: 400px;
  width: 100%;
}

.join-icon {
  width: 80px;
  height: 80px;
  margin: 0 auto 24px;
  border-radius: 50%;
  background: var(--td-success-color-light);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-success-color);
}

.join-title {
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 16px;
}

.join-message {
  font-size: 14px;
  color: var(--td-text-color-secondary);
  margin: 0 0 24px;
  
  &.error {
    color: var(--td-error-color);
  }
  
  &.success {
    color: var(--td-success-color);
  }
}
</style>
