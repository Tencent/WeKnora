import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getAgentById, updateAgent, type CustomAgent } from '@/api/agent'

const INSTALLER_AGENT_ID = 'builtin-skill-installer'
const LAST_CHAT_MODEL_KEY = 'weknora_last_chat_model_id'

function readLastChatModelID(): string {
  try {
    return localStorage.getItem(LAST_CHAT_MODEL_KEY) || ''
  } catch {
    return ''
  }
}

/** Each panel keeps its own draft while persisting to the same installer agent. */
export function useSkillInstallerModel() {
  const { t } = useI18n()
  const installerAgent = ref<Partial<CustomAgent> | null>(null)
  const installerModelId = ref('')
  const savingInstallerModel = ref(false)

  async function loadInstallerModel() {
    try {
      const res = await getAgentById(INSTALLER_AGENT_ID)
      installerAgent.value = res?.data || null
      const configured = installerAgent.value?.config?.model_id?.trim() || ''
      installerModelId.value = configured || readLastChatModelID()
    } catch {
      installerAgent.value = null
      installerModelId.value = readLastChatModelID()
    }
  }

  async function persistInstallerModel(modelId: string) {
    const id = modelId.trim()
    if (!id) throw new Error(t('settings.sandbox.skillInstallerModelRequired'))
    const current = installerAgent.value
    const config = { ...(current?.config || {}), model_id: id }
    const res = await updateAgent(INSTALLER_AGENT_ID, {
      name: current?.name || '',
      description: current?.description || '',
      avatar: current?.avatar || '',
      config,
    })
    installerAgent.value = res?.data || { ...current, config }
    installerModelId.value = id
  }

  async function onInstallerModelChange(modelId: string) {
    if (!modelId || modelId === '__add_model__') return
    installerModelId.value = modelId
    savingInstallerModel.value = true
    try {
      await persistInstallerModel(modelId)
    } catch (error: any) {
      MessagePlugin.error(error?.message || t('settings.sandbox.skillInstallerModelSaveFailed'))
    } finally {
      savingInstallerModel.value = false
    }
  }

  function resetInstallerModel() {
    installerAgent.value = null
    installerModelId.value = ''
  }

  return {
    installerModelId, savingInstallerModel, loadInstallerModel, persistInstallerModel,
    onInstallerModelChange, resetInstallerModel,
  }
}
