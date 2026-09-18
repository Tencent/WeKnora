<template>
  <div class="skill-install-cards">
    <div v-for="group in groups" :key="group.key" class="skill-install-group">
      <div v-if="group.temporary" class="skill-install-notice">
        <t-icon name="info-circle" class="skill-install-notice__icon" />
        <span>{{ group.temporaryName
          ? $t('agentStream.skillCards.temporaryNotice', { name: group.temporaryName })
          : $t('agentStream.skillCards.temporaryNoticeUnnamed') }}</span>
      </div>

      <div v-for="candidate in group.candidates" :key="candidate.source" class="skill-install-card">
        <div class="skill-install-card__head">
          <span class="skill-install-card__name" :title="candidate.name">
            {{ candidate.display_name || candidate.name }}
          </span>
          <t-tag size="small" variant="light" class="skill-install-card__tag">
            {{ registryLabel(candidate.registry) }}
          </t-tag>
          <t-tag v-if="candidate.official" size="small" variant="light" theme="success">
            {{ $t('agentStream.skillCards.official') }}
          </t-tag>
        </div>

        <p v-if="candidate.description" class="skill-install-card__desc">{{ candidate.description }}</p>

        <div class="skill-install-card__meta">
          <span v-if="candidate.owner">{{ candidate.owner }}</span>
          <span v-if="candidate.downloads">
            {{ $t('agentStream.skillCards.downloads', { count: formatCount(candidate.downloads) }) }}
          </span>
          <span v-if="candidate.installs">
            {{ $t('agentStream.skillCards.installs', { count: formatCount(candidate.installs) }) }}
          </span>
          <span v-if="candidate.file_count">
            {{ $t('agentStream.skillCards.files', { count: candidate.file_count }) }}
          </span>
          <code class="skill-install-card__source" :title="candidate.source">{{ candidate.source }}</code>
        </div>

        <div v-if="existingNote(candidate, group)" class="skill-install-card__note">
          {{ existingNote(candidate, group) }}
        </div>
        <div v-if="group.agentSelectsSkills && stateOf(group, candidate)?.phase === 'ready'"
          class="skill-install-card__note">
          {{ $t('agentStream.skillCards.selectInAgent') }}
        </div>

        <div class="skill-install-card__footer">
          <template v-if="stateOf(group, candidate)?.phase === 'installing'">
            <t-progress
              class="skill-install-card__progress"
              size="small"
              :percentage="percentOf(group, candidate) ?? 0"
              :label="false"
            />
            <span class="skill-install-card__status">
              {{ $t('agentStream.skillCards.installing', { percent: percentOf(group, candidate) ?? 0 }) }}
            </span>
          </template>
          <span v-else-if="stateOf(group, candidate)?.phase === 'ready'" class="skill-install-card__status is-success">
            <t-icon name="check-circle" />
            {{ group.newSessionsOnly ? $t('agentStream.skillCards.readyNewSession') : $t('agentStream.skillCards.ready') }}
          </span>
          <template v-else>
            <span v-if="stateOf(group, candidate)?.phase === 'failed'" class="skill-install-card__status is-error">
              {{ failureText(stateOf(group, candidate)?.error) }}
            </span>
            <t-tooltip v-if="!canInstall(group)" :content="installBlockedReason(group)" placement="top">
              <t-button size="small" variant="outline" disabled>{{ installLabel(candidate, group) }}</t-button>
            </t-tooltip>
            <t-button
              v-else
              size="small"
              theme="primary"
              :loading="stateOf(group, candidate)?.phase === 'registering'"
              :disabled="candidate.install_status === 'installing'"
              @click="confirmInstall(group, candidate)"
            >
              {{ installLabel(candidate, group) }}
            </t-button>
          </template>
          <t-button size="small" variant="text" @click="copySource(candidate.source)">
            {{ $t('agentStream.skillCards.copySource') }}
          </t-button>
          <t-link
            v-if="safeUrl(candidate.url)"
            size="small"
            theme="primary"
            hover="color"
            :href="safeUrl(candidate.url)"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ $t('agentStream.skillCards.viewSource') }}
          </t-link>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { copyWithToast } from '@/utils/clipboard'
import { useSkillInstallerModel } from '@/composables/useSkillInstallerModel'
import {
  chatSkillInstallPercent,
  chatSkillInstallState,
  installChatSkill,
  type ChatSkillInstallState,
} from '@/composables/useChatSkillInstall'
import type { SkillCardCandidate, SkillCardGroup } from '@/utils/skillInstallCards'

defineProps<{ groups: SkillCardGroup[] }>()

const { t } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()
const { installerModelId, loadInstallerModel, persistInstallerModel } = useSkillInstallerModel()

const REGISTRY_LABELS: Record<string, string> = {
  clawhub: 'ClawHub',
  'skills-sh': 'skills.sh',
  skillhub: 'SkillHub',
  github: 'GitHub',
  gitlab: 'GitLab',
}

function registryLabel(registry?: string): string {
  return (registry && REGISTRY_LABELS[registry]) || t('agentStream.skillCards.registryLink')
}

function formatCount(n: number): string {
  if (n >= 10000) return `${(n / 1000).toFixed(n >= 100000 ? 0 : 1)}k`
  return String(n)
}

function safeUrl(url?: string): string {
  if (!url) return ''
  try {
    const parsed = new URL(url)
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed.toString() : ''
  } catch {
    return ''
  }
}

function stateOf(group: SkillCardGroup, candidate: SkillCardCandidate): ChatSkillInstallState | undefined {
  return chatSkillInstallState(group.sandboxConfigId, candidate.source)
}

function percentOf(group: SkillCardGroup, candidate: SkillCardCandidate): number | null {
  return chatSkillInstallPercent(stateOf(group, candidate))
}

function canInstall(group: SkillCardGroup): boolean {
  return Boolean(group.sandboxConfigId) && authStore.hasRole('admin')
}

function installBlockedReason(group: SkillCardGroup): string {
  return group.sandboxConfigId
    ? t('agentStream.skillCards.adminOnly')
    : t('agentStream.skillCards.noSandbox')
}

function installLabel(candidate: SkillCardCandidate, group: SkillCardGroup): string {
  if (stateOf(group, candidate)?.phase === 'failed') return t('agentStream.skillCards.retry')
  if (candidate.install_status === 'ready') return t('agentStream.skillCards.reinstall')
  return t('agentStream.skillCards.install')
}

function existingNote(candidate: SkillCardCandidate, group: SkillCardGroup): string {
  if (stateOf(group, candidate)) return ''
  if (candidate.install_status === 'ready') return t('agentStream.skillCards.alreadyInstalled')
  if (candidate.install_status === 'installing') return t('agentStream.skillCards.alreadyInstalling')
  if (candidate.in_catalog) return t('agentStream.skillCards.sameNameInCatalog')
  return ''
}

function failureText(error?: string): string {
  return error
    ? t('agentStream.skillCards.failedWithReason', { reason: error })
    : t('agentStream.skillCards.failed')
}

function copySource(source: string) {
  void copyWithToast(source, 'agentStream.skillCards.sourceCopied')
}

// The installer is an agent too, and needs a model. The settings page asks
// for one up front; here the saved choice (or the last chat model) is used,
// and the user is sent to the settings page only when there is none.
async function ensureInstallerModel(configId: string): Promise<boolean> {
  await loadInstallerModel()
  if (!installerModelId.value) {
    MessagePlugin.warning(t('agentStream.skillCards.installerModelRequired'))
    uiStore.openSettings('skills', configId)
    return false
  }
  await persistInstallerModel(installerModelId.value)
  return true
}

function confirmInstall(group: SkillCardGroup, candidate: SkillCardCandidate) {
  const dialog = DialogPlugin.confirm({
    header: t('agentStream.skillCards.confirmTitle', { name: candidate.display_name || candidate.name }),
    body: t(group.newSessionsOnly
      ? 'agentStream.skillCards.confirmBodyNewSession'
      : 'agentStream.skillCards.confirmBody', { source: candidate.source }),
    confirmBtn: t('agentStream.skillCards.install'),
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      dialog.hide()
      try {
        if (!(await ensureInstallerModel(group.sandboxConfigId))) return
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('agentStream.skillCards.failed'))
        return
      }
      await installChatSkill(group.sandboxConfigId, candidate.source)
    },
  })
}
</script>

<style scoped lang="less">
.skill-install-cards {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
  margin: var(--app-space-2) 0;
}

.skill-install-group {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.skill-install-notice {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border-radius: var(--app-radius-md);
  background: var(--td-warning-color-1);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.55;

  &__icon {
    flex-shrink: 0;
    margin-top: 3px;
    color: var(--td-warning-color);
  }
}

.skill-install-card {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
  padding: var(--app-space-3);
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);

  &__head {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    min-width: 0;
  }

  &__name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--app-text-base);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__tag {
    flex-shrink: 0;
  }

  &__desc {
    margin: 0;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    font-size: var(--app-text-md);
    line-height: 1.55;
    color: var(--td-text-color-secondary);
  }

  &__meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--app-space-1) var(--app-space-3);
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }

  &__source {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--app-font-family-mono);
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }

  &__note {
    font-size: var(--app-text-sm);
    color: var(--td-warning-color);
  }

  &__footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--app-space-2);
    margin-top: var(--app-space-1);
  }

  &__progress {
    flex: 1 1 160px;
    max-width: 240px;
  }

  &__status {
    display: inline-flex;
    align-items: center;
    gap: var(--app-space-1);
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);

    &.is-success {
      color: var(--td-success-color);
    }

    &.is-error {
      color: var(--td-error-color);
    }
  }
}
</style>
