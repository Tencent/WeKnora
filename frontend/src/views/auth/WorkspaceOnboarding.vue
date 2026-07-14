<template>
  <main class="workspace-onboarding">
    <section class="workspace-card">
      <div class="workspace-mark" aria-hidden="true">
        <t-icon name="system-sum" size="30px" />
      </div>
      <h1>
        {{
          $t(
            authStore.canCreateTenant
              ? 'auth.workspaceOnboarding.title'
              : 'auth.workspaceOnboarding.inviteOnlyTitle',
          )
        }}
      </h1>
      <p class="workspace-description">
        {{
          $t(
            authStore.canCreateTenant
              ? 'auth.workspaceOnboarding.description'
              : 'auth.workspaceOnboarding.inviteOnlyDescription',
          )
        }}
      </p>

      <div v-if="policyLoading" class="policy-loading">
        <t-loading size="small" />
        <span>{{ $t('auth.workspaceOnboarding.loadingPolicy') }}</span>
      </div>
      <div v-else-if="policyLoadFailed" class="policy-error" role="alert">
        <t-icon name="error-circle" size="20px" aria-hidden="true" />
        <span>{{ $t('auth.workspaceOnboarding.policyLoadFailed') }}</span>
        <t-button size="small" variant="text" @click="loadPolicy">
          {{ $t('auth.workspaceOnboarding.retry') }}
        </t-button>
      </div>

      <template v-else>
        <div v-if="!authStore.canCreateTenant" class="invite-only-notice">
          <t-icon name="lock-on" size="20px" aria-hidden="true" />
          <span>{{ $t('auth.workspaceOnboarding.inviteOnlyNotice') }}</span>
        </div>

        <div class="workspace-actions" :class="{ 'workspace-actions--single': !authStore.canCreateTenant }">
          <t-button v-if="authStore.canCreateTenant" theme="primary" size="large" @click="createVisible = true">
            <template #icon><t-icon name="add" /></template>
            {{ $t('auth.workspaceOnboarding.create') }}
          </t-button>
          <t-button
            :theme="authStore.canCreateTenant ? 'default' : 'primary'"
            :variant="authStore.canCreateTenant ? 'outline' : 'base'"
            size="large"
            @click="invitationsVisible = true"
          >
            <template #icon><t-icon name="mail" /></template>
            {{ $t('auth.workspaceOnboarding.invitations') }}
            <template v-if="authStore.pendingInvitationCount > 0">
              ({{ authStore.pendingInvitationCount }})
            </template>
          </t-button>
        </div>
      </template>

      <p v-if="!policyLoading && !policyLoadFailed" class="workspace-help">
        {{
          $t(
            authStore.canCreateTenant
              ? 'auth.workspaceOnboarding.help'
              : 'auth.workspaceOnboarding.inviteOnlyHelp',
          )
        }}
      </p>
      <button class="logout-link" type="button" @click="handleLogout">
        {{ $t('auth.logout') }}
      </button>
    </section>

    <CreateTenantDialog v-model:visible="createVisible" @created="onTenantCreated" />
    <MyInvitationsDialog v-model:visible="invitationsVisible" />
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import CreateTenantDialog from '@/components/CreateTenantDialog.vue'
import MyInvitationsDialog from '@/components/MyInvitationsDialog.vue'
import { logout as logoutApi } from '@/api/auth'
import type { TenantInfo } from '@/api/tenant'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()
const createVisible = ref(false)
const invitationsVisible = ref(false)
const policyLoading = ref(true)
const policyLoadFailed = ref(false)

async function loadPolicy() {
  policyLoading.value = true
  policyLoadFailed.value = false
  try {
    const refreshed = await authStore.refreshFromAuthMe()
    if (!refreshed) {
      policyLoadFailed.value = true
      return
    }
    await authStore.fetchPendingInvitationCount()
  } finally {
    policyLoading.value = false
  }
}

onMounted(async () => {
  await loadPolicy()
})

watch(
  () => authStore.hasValidTenant,
  (ready) => {
    if (ready) router.replace('/platform/knowledge-bases')
  },
)

async function onTenantCreated(tenant: TenantInfo) {
  await authStore.refreshFromAuthMe()
  authStore.setSelectedTenant(tenant.id, tenant.name)
  await router.replace('/platform/knowledge-bases')
}

async function handleLogout() {
  await logoutApi()
  authStore.logout()
  await router.replace('/login')
}
</script>

<style scoped lang="less">
.workspace-onboarding {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 32px 20px;
  background:
    radial-gradient(circle at 20% 10%, color-mix(in srgb, var(--td-brand-color) 12%, transparent), transparent 38%),
    var(--td-bg-color-page);
}

.workspace-card {
  width: min(520px, 100%);
  padding: 44px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 20px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  text-align: center;
}

.workspace-mark {
  width: 64px;
  height: 64px;
  margin: 0 auto 22px;
  display: grid;
  place-items: center;
  border-radius: 18px;
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

h1 {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 26px;
  line-height: 1.3;
}

.workspace-description,
.workspace-help {
  color: var(--td-text-color-secondary);
  line-height: 1.7;
}

.workspace-description {
  margin: 14px 0 28px;
}

.workspace-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.workspace-actions--single {
  grid-template-columns: minmax(220px, 1fr);
}

.policy-loading,
.invite-only-notice,
.policy-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 52px;
  margin-bottom: 18px;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}

.policy-error {
  flex-wrap: wrap;
  padding: 12px 16px;
  border-radius: 10px;
  color: var(--td-error-color);
  background: var(--td-error-color-light);
}

.invite-only-notice {
  padding: 12px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-secondarycontainer);
  line-height: 1.5;
}

.invite-only-notice :deep(.t-icon) {
  flex: 0 0 auto;
  color: var(--td-text-color-secondary);
}

.workspace-help {
  margin: 24px 0 8px;
  font-size: 13px;
}

.logout-link {
  border: 0;
  padding: 6px 10px;
  color: var(--td-text-color-secondary);
  background: transparent;
  cursor: pointer;
}

.logout-link:hover {
  color: var(--td-brand-color);
}

@media (max-width: 560px) {
  .workspace-card {
    padding: 32px 22px;
  }

  .workspace-actions {
    grid-template-columns: 1fr;
  }
}
</style>
