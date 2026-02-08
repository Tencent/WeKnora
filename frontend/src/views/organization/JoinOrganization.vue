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
  background: linear-gradient(135deg, #f7f9fc 0%, #e6f7ec 100%);
  padding: 20px;
}

.join-card {
  background: #fff;
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
  background: #e6f7ec;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #00a870;
}

.join-title {
  font-size: 20px;
  font-weight: 600;
  color: #1d2129;
  margin: 0 0 16px;
}

.join-message {
  font-size: 14px;
  color: #86909c;
  margin: 0 0 24px;
  
  &.error {
    color: #e34d59;
  }
  
  &.success {
    color: #00a870;
  }
}
</style>
