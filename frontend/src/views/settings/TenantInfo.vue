<template>
  <div class="tenant-info">
    <div class="section-header">
      <h2>{{ $t('tenant.title') }}</h2>
      <p class="section-description">{{ $t('tenant.sectionDescription') }}</p>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenant.loadingInfo') }}</span>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadInfo">{{ $t('tenant.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <!-- Content -->
    <div v-else class="settings-group">
      <!-- Tenant ID -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.idLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.idDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo?.id || '-' }}</span>
        </div>
      </div>

      <!-- Tenant name -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.nameLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.nameDescription') }}</p>
        </div>
        <div class="setting-control">
          <!-- 只读态：显示名称 + 编辑按钮（owner 才看得见编辑入口）。
               原地编辑取代弹窗：少一层视觉打断，与其它行的展示节奏一致。 -->
          <template v-if="!editing">
            <span class="info-value">{{ tenantInfo?.name || '-' }}</span>
            <t-button v-if="canEditTenant" theme="default" variant="text" size="small" class="edit-btn"
              @click="startEditName">
              <template #icon>
                <t-icon name="edit" />
              </template>
              {{ $t('tenant.details.editName') }}
            </t-button>
          </template>
          <!-- 编辑态：输入框 + 保存/取消。回车保存，Esc 取消。 -->
          <div v-else class="inline-edit">
            <t-input v-model="editName" :placeholder="$t('tenant.details.editNamePlaceholder')" :maxlength="64"
              :disabled="saving" autofocus class="inline-edit-input" @enter="saveTenantName"
              @keydown="onEditKeydown" />
            <t-button theme="primary" size="small" :loading="saving" :disabled="!canSubmit" @click="saveTenantName">
              {{ $t('tenant.details.editNameConfirm') }}
            </t-button>
            <t-button theme="default" variant="outline" size="small" :disabled="saving" @click="cancelEditName">
              {{ $t('tenant.details.editNameCancel') }}
            </t-button>
          </div>
        </div>
      </div>

      <!-- Tenant description -->
      <div v-if="tenantInfo?.description" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.descriptionLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.descriptionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo.description }}</span>
        </div>
      </div>

      <!-- Tenant business -->
      <div v-if="tenantInfo?.business" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.businessLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.businessDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ tenantInfo.business }}</span>
        </div>
      </div>

      <!-- Tenant status -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.statusLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.statusDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-tag :theme="getStatusTheme(tenantInfo?.status)" variant="light" size="small">
            {{ getStatusText(tenantInfo?.status) }}
          </t-tag>
        </div>
      </div>

      <!-- Tenant creation time -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.details.createdAtLabel') }}</label>
          <p class="desc">{{ $t('tenant.details.createdAtDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatDate(tenantInfo?.created_at) }}</span>
        </div>
      </div>

      <!-- Storage quota -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.quotaLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.quotaDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatBytes(tenantInfo.storage_quota) }}</span>
        </div>
      </div>

      <!-- Used storage -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.usedLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.usedDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatBytes(tenantInfo.storage_used || 0) }}</span>
        </div>
      </div>

      <!-- Storage usage -->
      <div v-if="tenantInfo?.storage_quota !== undefined" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.storage.usageLabel') }}</label>
          <p class="desc">{{ $t('tenant.storage.usageDescription') }}</p>
        </div>
        <div class="setting-control">
          <div class="usage-control">
            <span class="usage-text">{{ getUsagePercentage() }}%</span>
            <t-progress :percentage="getUsagePercentage()" :show-info="false" size="small"
              :theme="getUsagePercentage() > 80 ? 'warning' : 'success'" style="flex: 1;" />
          </div>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { getCurrentUser, type TenantInfo } from '@/api/auth'
import { updateTenant as updateTenantApi } from '@/api/tenant'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const authStore = useAuthStore()

// Reactive state
const tenantInfo = ref<TenantInfo | null>(null)
const loading = ref(true)
const error = ref('')

// 仅 owner 可改租户名（与后端 router.go 中 g.Owner() 守卫一致；
// 服务端始终是权限的最终裁判，这里只决定 UI 是否露出入口）。
const canEditTenant = computed(() => authStore.hasRole('owner'))

// 原地编辑租户名称：editing 控制行内只读 / 编辑两种形态切换。
// 不沿用 dialog 是因为这里只有一个字段，弹窗反而打断了配置浏览节奏。
const editing = ref(false)
const editName = ref('')
const saving = ref(false)
const editNameTrimmed = computed(() => editName.value.trim())
// 保存按钮可点条件：非空、改了内容、不在保存中。
// 后端 name 字段没有 uniqueIndex 也没有重名校验，所以这里不做"是否已存在"的判断；
// 后端 service 也只在 create 时拒空，update 时不校验，保持前端兜底非空即可。
const canSubmit = computed(
  () => !saving.value && !!editNameTrimmed.value && editNameTrimmed.value !== tenantInfo.value?.name,
)

const startEditName = () => {
  editName.value = tenantInfo.value?.name || ''
  editing.value = true
}

const cancelEditName = () => {
  if (saving.value) return
  editing.value = false
  editName.value = ''
}

// t-input 自身不冒泡 esc，这里手动处理（与 enter 的体验对称）。
const onEditKeydown = (_value: any, ctx: { e: KeyboardEvent }) => {
  if (ctx?.e?.key === 'Escape') {
    cancelEditName()
  }
}

const saveTenantName = async () => {
  const newName = editNameTrimmed.value
  if (!newName) {
    MessagePlugin.warning(t('tenant.details.editNameRequired'))
    return
  }
  if (!tenantInfo.value?.id) return
  if (newName === tenantInfo.value.name) {
    editing.value = false
    return
  }

  try {
    saving.value = true
    const resp = await updateTenantApi(Number(tenantInfo.value.id), { name: newName })
    if (resp.success) {
      // 本地立即回显，避免等 /auth/me 往返；同步刷新登录态里的 tenant
      // 缓存（若当前激活租户就是 home tenant，顶部租户切换器等地方也跟着更新）。
      if (tenantInfo.value) {
        tenantInfo.value = { ...tenantInfo.value, name: newName }
      }
      if (authStore.tenant && String(authStore.tenant.id) === String(tenantInfo.value?.id)) {
        authStore.setTenant({ ...authStore.tenant, name: newName })
      }
      // memberships 里的 tenant_name 是租户切换器读的字段，一并同步避免显示旧名字。
      if (authStore.memberships?.length) {
        const next = authStore.memberships.map((m) =>
          String(m.tenant_id) === String(tenantInfo.value?.id)
            ? { ...m, tenant_name: newName }
            : m,
        )
        authStore.setMemberships(next)
      }
      MessagePlugin.success(t('tenant.details.editNameSuccess'))
      editing.value = false
    } else {
      MessagePlugin.error(resp.message || t('tenant.details.editNameFailed'))
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('tenant.details.editNameFailed'))
  } finally {
    saving.value = false
  }
}

// Methods
const loadInfo = async () => {
  try {
    loading.value = true
    error.value = ''

    const userResponse = await getCurrentUser()

    if ((userResponse as any).success && userResponse.data) {
      tenantInfo.value = userResponse.data.tenant
    } else {
      error.value = userResponse.message || t('tenant.messages.fetchFailed')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenant.messages.networkError')
  } finally {
    loading.value = false
  }
}

const getStatusText = (status: string | undefined) => {
  switch (status) {
    case 'active':
      return t('tenant.statusActive')
    case 'inactive':
      return t('tenant.statusInactive')
    case 'suspended':
      return t('tenant.statusSuspended')
    default:
      return t('tenant.statusUnknown')
  }
}

const getStatusTheme = (status: string | undefined) => {
  switch (status) {
    case 'active':
      return 'success'
    case 'inactive':
      return 'warning'
    case 'suspended':
      return 'danger'
    default:
      return 'default'
  }
}

const formatDate = (dateStr: string | undefined) => {
  if (!dateStr) return t('tenant.unknown')

  try {
    const date = new Date(dateStr)
    const formatter = new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    })
    return formatter.format(date)
  } catch {
    return t('tenant.formatError')
  }
}

const formatBytes = (bytes: number) => {
  if (bytes === 0) return '0 B'

  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))

  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

const getUsagePercentage = () => {
  if (!tenantInfo.value?.storage_quota || tenantInfo.value.storage_quota === 0) {
    return 0
  }

  const used = tenantInfo.value.storage_used || 0
  const percentage = (used / tenantInfo.value.storage_quota) * 100
  return Math.min(Math.round(percentage * 100) / 100, 100)
}

// Lifecycle
onMounted(() => {
  loadInfo()
})
</script>

<style lang="less" scoped>
.tenant-info {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.loading-inline {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 40px 0;
  justify-content: center;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}

.error-inline {
  padding: 20px 0;
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;

  .info-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
    text-align: right;
    word-break: break-word;
  }

  .edit-btn {
    flex-shrink: 0;
  }
}

.inline-edit {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  justify-content: flex-end;
}

.inline-edit-input {
  /* 行内编辑场景下输入框不能撑满整行，否则右侧两个按钮会贴边；
     给一个合理上限即可，超出走 t-input 自己的省略。 */
  max-width: 220px;
  flex: 1;
}

.usage-control {
  //   width: 100%;
  //   display: flex;
  //   align-items: center;
  //   gap: 12px;

  .usage-text {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    min-width: 50px;
    text-align: right;
  }
}
</style>
