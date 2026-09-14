import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'

interface DocumentPictureInPicture {
  requestWindow(options: { width: number; height: number }): Promise<Window>
}

/** A single Vue Teleport destination, owned by the mounted task preview. */
export function useDocumentPictureInPicture(selected: Ref<boolean>, title: () => string) {
  const api = (window as Window & { documentPictureInPicture?: DocumentPictureInPicture }).documentPictureInPicture
  const supported = !!api && window.isSecureContext && window.top === window
  const target = shallowRef<HTMLElement | null>(null)
  const opening = ref(false)
  let ownedWindow: Window | null = null
  let disposeStyles: (() => void) | undefined
  let generation = 0
  let disposed = false

  function close() {
    generation++
    opening.value = false
    target.value = null
    disposeStyles?.()
    disposeStyles = undefined
    const previous = ownedWindow
    ownedWindow = null
    previous?.close()
  }

  async function open() {
    if (!supported || !api || disposed || !selected.value || opening.value || ownedWindow) return
    const current = ++generation
    opening.value = true
    let created: Window | undefined
    try {
      // Keep this call synchronous with the button click (transient activation).
      created = await api.requestWindow({ width: 360, height: 440 })
      if (disposed || current !== generation || !selected.value || created.closed) {
        created.close()
        return
      }
      ownedWindow = created
      disposeStyles = copyPreviewStyles(document, created.document)
      created.document.title = title()
      const pip = created
      pip.addEventListener('pagehide', () => {
        if (ownedWindow === pip) close()
      }, { once: true })
      target.value = pip.document.body
    } catch (error) {
      created?.close()
      if (!disposed && current === generation) {
        close()
        throw error
      }
    } finally {
      if (current === generation) opening.value = false
    }
  }

  watch(selected, value => { if (!value) close() }, { flush: 'sync' })
  watch(title, value => { if (ownedWindow) ownedWindow.document.title = value })
  onBeforeUnmount(() => { disposed = true; close() })
  return { supported, target, opening, open, close }
}

function copyPreviewStyles(source: Document, destination: Document) {
  const base = destination.createElement('base')
  base.href = source.baseURI
  destination.head.append(base)
  for (const sheet of Array.from(source.styleSheets)) {
    if (sheet.disabled) continue
    // Keep linked CSS at its original URL so relative font/image URLs resolve.
    if (sheet.href) {
      const link = destination.createElement('link')
      link.rel = 'stylesheet'
      link.href = sheet.href
      link.media = sheet.media.mediaText
      link.disabled = sheet.disabled
      destination.head.append(link)
    } else {
      const style = destination.createElement('style')
      style.textContent = Array.from(sheet.cssRules, rule => rule.cssText).join('\n')
      style.media = sheet.media.mediaText
      destination.head.append(style)
    }
  }
  const syncTheme = () => {
    for (const [from, to] of [[source.documentElement, destination.documentElement], [source.body, destination.body]]) {
      for (const name of ['class', 'style', 'lang', 'dir', 'theme-mode', 'tdesign-theme', 'data-theme']) {
        const value = from!.getAttribute(name)
        if (value === null) to!.removeAttribute(name)
        else to!.setAttribute(name, value)
      }
    }
  }
  syncTheme()
  const observer = new MutationObserver(syncTheme)
  observer.observe(source.documentElement, { attributes: true })
  observer.observe(source.body, { attributes: true })
  const reset = destination.createElement('style')
  reset.textContent = 'html, body { margin: 0 !important; padding: 0 !important; width: 100% !important; height: 100% !important; overflow: auto !important; background: var(--td-bg-color-container); }'
  destination.head.append(reset)
  return () => observer.disconnect()
}
