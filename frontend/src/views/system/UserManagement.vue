<template>
  <div class="user-management">
    <header class="section-header">
      <div class="section-header__titlewrap">
        <h2>{{ t('userManagement.title') }}</h2>
      </div>
      <p class="section-description">{{ t('userManagement.pageHint') }}</p>
    </header>

    <t-tabs v-model="activeTab" class="user-management__tabs">
      <t-tab-panel value="local" :label="t('userManagement.tabs.local')">
        <LocalUsersTab />
      </t-tab-panel>
      <t-tab-panel value="oidc" :label="t('userManagement.tabs.oidc')">
        <OidcTab />
      </t-tab-panel>
      <t-tab-panel value="ldap" :label="t('userManagement.tabs.ldap')">
        <LdapTab />
      </t-tab-panel>
    </t-tabs>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocalUsersTab from './user-management/LocalUsersTab.vue'
import OidcTab from './user-management/OidcTab.vue'
import LdapTab from './user-management/LdapTab.vue'

const { t } = useI18n()

// 本地用户 is the landing tab: it is the screen an admin opens most often, and
// the two provider tabs are only revisited when an IdP changes.
const activeTab = ref<'local' | 'oidc' | 'ldap'>('local')
</script>

<style scoped>
.user-management {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section-header {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.section-header__titlewrap h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-description {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.user-management__tabs {
  margin-top: 4px;
}
</style>
