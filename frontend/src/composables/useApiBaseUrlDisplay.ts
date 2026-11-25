import { computed, onMounted, ref } from 'vue'
import { getAbsoluteApiBaseUrl } from '@/utils/api-base'

type WeKnoraDesktopWindow = Window & {
  __WEKNORA_API_BASE__?: string
  go?: {
    main?: {
      App?: {
        GetAPIBaseURL?: () => Promise<string> | string
      }
    }
  }
}

export function useApiBaseUrlDisplay() {
  const wailsApiBaseURL = ref<string | null>(null)

  const apiBaseUrlDisplay = computed(() => {
    if (wailsApiBaseURL.value) {
      return wailsApiBaseURL.value
    }
    // Resolve VITE BASE_URL against the page origin so a sub-path deployment
    // (e.g. `/app/weknora/`) still shows a full, copy-pasteable URL.
    return getAbsoluteApiBaseUrl()
  })

  async function loadApiBaseUrl() {
    const win = window as WeKnoraDesktopWindow
    for (let i = 0; i < 40; i++) {
      const injected = win.__WEKNORA_API_BASE__
      if (typeof injected === 'string' && injected.trim()) {
        wailsApiBaseURL.value = injected.trim().replace(/\/$/, '')
        return
      }
      const fn = win.go?.main?.App?.GetAPIBaseURL
      if (typeof fn === 'function') {
        try {
          const raw = await Promise.resolve(fn())
          if (typeof raw === 'string' && raw.trim()) {
            wailsApiBaseURL.value = raw.trim().replace(/\/$/, '')
            return
          }
        } catch {
          /* binding error */
        }
      }
      await new Promise<void>((resolve) => setTimeout(resolve, 50))
    }
  }

  onMounted(() => {
    void loadApiBaseUrl()
  })

  return { apiBaseUrlDisplay }
}
