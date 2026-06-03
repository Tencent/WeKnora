import type { SpotlightGuideStep } from '@/types/spotlightGuide'

export const GLOBAL_USER_GUIDE_KEY = 'weknora:new-user-guide-done:v1'
export const OPEN_NEW_USER_GUIDE_EVENT = 'weknora:open-new-user-guide'

export function openNewUserGuide() {
  window.dispatchEvent(new CustomEvent(OPEN_NEW_USER_GUIDE_EVENT))
}

export const KB_EDITOR_FOCUS_SECTION_EVENT = 'weknora:kb-editor-focus-section'
export const AGENT_EDITOR_FOCUS_SECTION_EVENT = 'weknora:agent-editor-focus-section'

export type ContextualGuideTourId =
  | 'kbList'
  | 'kbCreate'
  | 'kbDetail'
  | 'chat'
  | 'tenantModels'
  | 'agentList'
  | 'agentCreate'

const focusKbEditorSection = (section: string) => {
  window.dispatchEvent(
    new CustomEvent(KB_EDITOR_FOCUS_SECTION_EVENT, { detail: { section } }),
  )
}

const focusKbEditorBasic = () => focusKbEditorSection('basic')

export interface ContextualGuideTourConfig {
  storageKey: string
  stepI18nPrefix: string
  steps: SpotlightGuideStep[]
  /** 首次展示前的延迟（毫秒） */
  openDelayMs: number
  /** 完成本引导时一并标记为已完成的其他引导 */
  alsoCompleteTours?: ContextualGuideTourId[]
}

export const CONTEXTUAL_GUIDE_TOURS: Record<ContextualGuideTourId, ContextualGuideTourConfig> = {
  kbList: {
    storageKey: 'weknora:contextual-guide-kb-list:v2',
    stepI18nPrefix: 'contextualGuide.kbList.steps',
    openDelayMs: 500,
    steps: [
      {
        key: 'create',
        // 空列表时优先高亮居中的主 CTA，否则退化为顶栏新建按钮
        target: '.empty-state-btn[data-guide="kb-list-create"], [data-guide="kb-list-create"]',
        placement: 'bottom',
        interact: true,
      },
    ],
  },
  // 步骤由 KbCreateContextualGuide.vue 按文档库/FAQ 动态组装
  kbCreate: {
    storageKey: 'weknora:contextual-guide-kb-create:v3',
    stepI18nPrefix: 'contextualGuide.kbCreate.steps',
    openDelayMs: 450,
    alsoCompleteTours: ['kbList'],
    steps: [],
  },
  tenantModels: {
    storageKey: 'weknora:contextual-guide-tenant-models:v1',
    stepI18nPrefix: 'contextualGuide.tenantModels.steps',
    openDelayMs: 500,
    steps: [],
  },
  kbDetail: {
    storageKey: 'weknora:contextual-guide-kb-detail:v1',
    stepI18nPrefix: 'contextualGuide.kbDetail.steps',
    openDelayMs: 600,
    steps: [
      {
        key: 'intro',
      },
      {
        key: 'upload',
        target: '[data-guide="kb-detail-add-doc"]',
        placement: 'bottom',
      },
      { key: 'done' },
    ],
  },
  chat: {
    storageKey: 'weknora:contextual-guide-chat:v1',
    stepI18nPrefix: 'contextualGuide.chat.steps',
    openDelayMs: 800,
    steps: [
      {
        key: 'kb',
        target: '[data-guide="chat-kb-mention"]',
        placement: 'top',
        optional: true,
      },
      {
        key: 'input',
        target: '[data-guide="chat-input"]',
        placement: 'top',
      },
      {
        key: 'send',
        target: '[data-guide="chat-send"]',
        placement: 'top',
      },
      { key: 'done' },
    ],
  },
  agentList: {
    storageKey: 'weknora:contextual-guide-agent-list:v1',
    stepI18nPrefix: 'contextualGuide.agentList.steps',
    openDelayMs: 500,
    steps: [
      {
        key: 'create',
        target: '.empty-state-btn[data-guide="agent-list-create"], [data-guide="agent-list-create"]',
        placement: 'bottom',
        interact: true,
      },
    ],
  },
  agentCreate: {
    storageKey: 'weknora:contextual-guide-agent-create:v1',
    stepI18nPrefix: 'contextualGuide.agentCreate.steps',
    openDelayMs: 450,
    alsoCompleteTours: ['agentList'],
    steps: [],
  },
}

export function isContextualGuideDone(tourId: ContextualGuideTourId): boolean {
  return localStorage.getItem(CONTEXTUAL_GUIDE_TOURS[tourId].storageKey) === '1'
}

export function markContextualGuideDone(tourId: ContextualGuideTourId) {
  const config = CONTEXTUAL_GUIDE_TOURS[tourId]
  localStorage.setItem(config.storageKey, '1')
  config.alsoCompleteTours?.forEach((id) => {
    localStorage.setItem(CONTEXTUAL_GUIDE_TOURS[id].storageKey, '1')
  })
}

export function isGlobalUserGuideDone(): boolean {
  return localStorage.getItem(GLOBAL_USER_GUIDE_KEY) === '1'
}
