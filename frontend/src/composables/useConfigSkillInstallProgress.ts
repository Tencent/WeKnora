import { ref } from 'vue'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import {
  configSkillInstallEventsUrl,
  type ConfigSkillInstallEvent,
} from '@/api/system'
import i18n from '@/i18n'
import { generateRandomString } from '@/utils'
import { getApiBaseUrl } from '@/utils/api-base'
import {
  liveInstallPercent,
  progressKey,
  type SkillInstallProgressTarget,
} from './skillInstallProgress'

export type { SkillInstallProgressTarget } from './skillInstallProgress'
export {
  clampInstallPercent,
  formatBusyInstallStatus,
  liveInstallPercent,
  progressKey,
} from './skillInstallProgress'

export function useConfigSkillInstallProgress(options?: {
  onDone?: (target: SkillInstallProgressTarget, event: ConfigSkillInstallEvent) => void
}) {
  const progressByKey = ref<Record<string, ConfigSkillInstallEvent>>({})
  const abortByKey = new Map<string, AbortController>()

  function eventOf(configId: string, skillId: string): ConfigSkillInstallEvent | undefined {
    return progressByKey.value[progressKey(configId, skillId)]
  }

  function percentOf(configId: string, skillId: string): number | null {
    return liveInstallPercent(eventOf(configId, skillId))
  }

  function forget(key: string) {
    if (!(key in progressByKey.value)) return
    const next = { ...progressByKey.value }
    delete next[key]
    progressByKey.value = next
  }

  function stop(key: string) {
    const controller = abortByKey.get(key)
    if (!controller) return
    controller.abort()
    abortByKey.delete(key)
  }

  function stopAll() {
    for (const key of [...abortByKey.keys()]) stop(key)
  }

  function follow(configId: string, skillId: string) {
    const key = progressKey(configId, skillId)
    if (!configId || !skillId || abortByKey.has(key)) return
    const existing = progressByKey.value[key]
    // A finished stream (success, failure, or no Redis) must not be reopened
    // on every catalog poll. Detached is the exception: the run is still going.
    if (existing?.done && existing.stage !== 'detached') return
    forget(key)
    const controller = new AbortController()
    abortByKey.set(key, controller)

    const token = localStorage.getItem('weknora_token')
    const tenantId = localStorage.getItem('weknora_selected_tenant_id')
    const url = `${getApiBaseUrl()}${configSkillInstallEventsUrl(configId, skillId)}`

    void fetchEventSource(url, {
      method: 'GET',
      headers: {
        Authorization: token ? `Bearer ${token}` : '',
        'Accept-Language': i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN',
        'X-Request-ID': generateRandomString(12),
        ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
      },
      signal: controller.signal,
      openWhenHidden: true,
      onmessage(ev) {
        if (!ev.data) return
        let parsed: ConfigSkillInstallEvent
        try {
          parsed = JSON.parse(ev.data) as ConfigSkillInstallEvent
        } catch {
          return
        }
        progressByKey.value = { ...progressByKey.value, [key]: parsed }
        if (parsed.done) {
          stop(key)
          options?.onDone?.({ configId, skillId }, parsed)
        }
      },
      onerror() {
        stop(key)
        throw new Error('skill install stream closed')
      },
    }).catch(() => {
      stop(key)
    })
  }

  function sync(targets: SkillInstallProgressTarget[]) {
    const wanted = new Set<string>()
    for (const target of targets) {
      const configId = target.configId.trim()
      const skillId = target.skillId.trim()
      if (!configId || !skillId) continue
      const key = progressKey(configId, skillId)
      wanted.add(key)
      follow(configId, skillId)
    }
    for (const key of [...abortByKey.keys()]) {
      if (!wanted.has(key)) stop(key)
    }
    for (const key of Object.keys(progressByKey.value)) {
      if (!wanted.has(key)) forget(key)
    }
  }

  return { progressByKey, eventOf, percentOf, follow, sync, stopAll }
}
