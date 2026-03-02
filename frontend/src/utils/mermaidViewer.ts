/**
 * Mermaid 图表全屏查看器
 * 支持：点击放大、滚轮缩放、鼠标拖拽、高清导出
 */

/**
 * 下载 SVG 为 PNG 图片（使用实际渲染尺寸）
 */
const downloadSvgAsImage = async (svgElement: SVGElement, filename = 'mermaid-diagram.png'): Promise<void> => {
  // 获取 SVG 实际渲染尺寸
  const bbox = svgElement.getBoundingClientRect()
  const w = Math.round(bbox.width)
  const h = Math.round(bbox.height)

  // 克隆 SVG
  const svgClone = svgElement.cloneNode(true) as SVGElement
  svgClone.setAttribute('width', String(w))
  svgClone.setAttribute('height', String(h))

  const svgData = new XMLSerializer().serializeToString(svgClone)
  const svgDataUri = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svgData)

  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => {
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, w, h)
      ctx.drawImage(img, 0, 0, w, h)

      canvas.toBlob((blob) => {
        if (!blob) return
        const link = document.createElement('a')
        link.download = filename
        link.href = URL.createObjectURL(blob)
        link.click()
        URL.revokeObjectURL(link.href)
        resolve()
      }, 'image/png')
    }
    img.src = svgDataUri
  })
}

/**
 * 显示按钮操作反馈提示
 */
const showBtnFeedback = (btn: HTMLElement, success: boolean, text?: string): void => {
  const origColor = btn.style.color
  const origTitle = btn.title
  btn.style.color = success ? '#07c05f' : '#ef4444'
  btn.title = text || (success ? '成功' : '失败')
  setTimeout(() => {
    btn.style.color = origColor
    btn.title = origTitle
  }, 1500)
}

/**
 * 打开 Mermaid 全屏查看器
 */
export const openMermaidFullscreen = (svgHtml: string): void => {
  let scale = 1
  let translateX = 0
  let translateY = 0
  let isDragging = false
  let dragStartX = 0
  let dragStartY = 0
  let dragStartTX = 0
  let dragStartTY = 0
  const STEP = 0.2

  // 创建遮罩层
  const overlay = document.createElement('div')
  overlay.style.cssText = 'position:fixed;inset:0;zIndex:9999;background:rgba(0,0,0,0.65);overflow:hidden;cursor:grab;'

  // 创建工具栏
  const toolbar = document.createElement('div')
  toolbar.style.cssText = 'position:fixed;top:20px;right:20px;display:flex;gap:6px;zIndex:10001;'

  const createBtn = (title: string, icon: string): HTMLButtonElement => {
    const btn = document.createElement('button')
    btn.title = title
    btn.style.cssText = 'display:flex;align-items:center;justify-content:center;width:36px;height:36px;border:1px solid #e5e7eb;border-radius:6px;background:rgba(255,255,255,0.95);color:#6b7280;cursor:pointer;padding:0;box-shadow:0 2px 8px rgba(0,0,0,0.15);'
    btn.innerHTML = icon
    btn.onmouseenter = () => { btn.style.background = '#f0fdf4'; btn.style.color = '#07c05f' }
    btn.onmouseleave = () => { btn.style.background = 'rgba(255,255,255,0.95)'; btn.style.color = '#6b7280' }
    return btn
  }

  const zoomInBtn = createBtn('放大', '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="11" y1="8" x2="11" y2="14"/><line x1="8" y1="11" x2="14" y2="11"/></svg>')
  const zoomOutBtn = createBtn('缩小', '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="8" y1="11" x2="14" y2="11"/></svg>')
  const resetBtn = createBtn('重置', '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>')
  const downloadBtn = createBtn('下载图片', '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>')
  const closeBtn = createBtn('关闭', '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>')
  toolbar.append(zoomInBtn, zoomOutBtn, resetBtn, downloadBtn, closeBtn)

  // 创建内容区域
  const content = document.createElement('div')
  content.style.cssText = 'position:absolute;left:50%;top:50%;background:#fff;border-radius:12px;padding:32px;box-shadow:0 8px 32px rgba(0,0,0,0.2);transformOrigin:0 0;'
  content.innerHTML = svgHtml
  const svgEl = content.querySelector('svg')
  if (svgEl) {
    svgEl.style.display = 'block'
    svgEl.setAttribute('draggable', 'false')
  }

  overlay.appendChild(toolbar)
  overlay.appendChild(content)
  document.body.appendChild(overlay)

  // 自动适配大小
  const margin = 60
  const viewW = window.innerWidth - margin * 2
  const viewH = window.innerHeight - margin * 2
  if (content.offsetWidth > 0 && content.offsetHeight > 0) {
    const fitScale = Math.min(viewW / content.offsetWidth, viewH / content.offsetHeight)
    scale = Math.max(0.5, Math.min(fitScale, 10))
  }

  // 应用变换
  const applyTransform = () => {
    content.style.transform = `translate(calc(-50% + ${translateX}px), calc(-50% + ${translateY}px)) scale(${scale})`
  }
  applyTransform()

  // 缩放按钮事件
  zoomInBtn.onclick = (e) => { e.stopPropagation(); scale = Math.min(10, scale + STEP); applyTransform() }
  zoomOutBtn.onclick = (e) => { e.stopPropagation(); scale = Math.max(0.2, scale - STEP); applyTransform() }
  resetBtn.onclick = (e) => { e.stopPropagation(); scale = 1; translateX = 0; translateY = 0; applyTransform() }

  // 下载 - 使用实际渲染尺寸
  downloadBtn.onclick = (e) => {
    e.stopPropagation()
    if (!svgEl) return
    downloadSvgAsImage(svgEl)
    showBtnFeedback(downloadBtn, true, '下载中...')
  }

  // 关闭函数
  let isClosed = false
  const close = () => {
    if (isClosed) return
    isClosed = true
    window.removeEventListener('mousemove', onMouseMove)
    window.removeEventListener('mouseup', onMouseUp)
    document.removeEventListener('keydown', onEsc)
    overlay.remove()
  }

  closeBtn.onclick = (e) => { e.stopPropagation(); close() }

  const onEsc = (e: KeyboardEvent) => {
    if (e.key === 'Escape') close()
  }
  document.addEventListener('keydown', onEsc)

  // 滚轮缩放
  overlay.onwheel = (e) => {
    e.preventDefault()
    const oldScale = scale
    scale = e.deltaY < 0 ? Math.min(10, scale + STEP) : Math.max(0.2, scale - STEP)
    const rect = overlay.getBoundingClientRect()
    const mx = e.clientX - rect.left - rect.width / 2
    const my = e.clientY - rect.top - rect.height / 2
    const ratio = 1 - scale / oldScale
    translateX += (mx - translateX) * ratio
    translateY += (my - translateY) * ratio
    applyTransform()
  }

  // 拖拽
  const onMouseMove = (e: MouseEvent) => {
    if (!isDragging) return
    translateX = dragStartTX + (e.clientX - dragStartX)
    translateY = dragStartTY + (e.clientY - dragStartY)
    applyTransform()
  }

  const onMouseUp = () => {
    isDragging = false
    overlay.style.cursor = 'grab'
  }

  overlay.onmousedown = (e) => {
    const target = e.target as Element
    if (target.closest('button')) return
    isDragging = true
    dragStartX = e.clientX
    dragStartY = e.clientY
    dragStartTX = translateX
    dragStartTY = translateY
    overlay.style.cursor = 'grabbing'
    e.preventDefault()
  }

  // 点击遮罩层关闭
  overlay.onclick = (e) => {
    const target = e.target as Element
    if (target === overlay) {
      close()
    }
  }

  window.addEventListener('mousemove', onMouseMove)
  window.addEventListener('mouseup', onMouseUp)
}

/**
 * 为 Mermaid 图表绑定点击全屏事件
 */
export const bindMermaidClickEvents = (container: HTMLElement): void => {
  if (!container) return
  const mermaidDivs = container.querySelectorAll('.mermaid')
  mermaidDivs.forEach((div) => {
    const divEl = div as HTMLElement
    divEl.style.cursor = 'pointer'
    const clickHandler = (e: Event) => {
      e.stopPropagation()
      e.preventDefault()
      const svg = divEl.querySelector('svg')
      if (svg) {
        openMermaidFullscreen(svg.outerHTML)
      }
    }
    divEl.removeEventListener('click', clickHandler)
    divEl.addEventListener('click', clickHandler)
  })
}
