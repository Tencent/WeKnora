import { onBeforeUnmount, onMounted } from 'vue'
import { DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

/**
 * 全屏"设置类"弹窗（Settings / AgentEditor / OrganizationSettings / KnowledgeBaseEditor）
 * 共用的壳层交互：
 *   - Esc 关闭（当有 TDesign 弹窗 / 抽屉 / 弹层叠在上面时不响应，让它们先关）
 *   - 遮罩点击关闭
 *   - 有未保存更改时先弹二次确认
 *
 * 用法：
 *   const shell = useModalShell({
 *     visible: () => props.visible,
 *     close: () => emit('update:visible', false),
 *     snapshot: () => formData.value,   // 可选：参与 dirty 比较的数据
 *   })
 *   // 数据加载完成 / 保存成功后：shell.markClean()
 *   // 模板：@click.self="shell.requestClose"，关闭按钮 @click="shell.requestClose"
 */
export interface ModalShellOptions {
  visible: () => boolean
  close: () => void
  /** 参与 dirty 比较的数据；不传则不做未保存提示 */
  snapshot?: () => unknown
  /** 返回 true 时跳过 Esc（例如组件内部自绘的弹层正打开） */
  ignoreEscape?: () => boolean
}

function serialize(value: unknown): string {
  try {
    return JSON.stringify(value ?? null)
  } catch {
    return ''
  }
}

/** 页面上是否有 TDesign 的弹窗 / 抽屉 / 弹层处于打开状态 */
function hasOpenTDesignOverlay(): boolean {
  if (typeof document === 'undefined') return false
  const dialogCtx = document.querySelector<HTMLElement>('.t-dialog__ctx')
  if (dialogCtx && dialogCtx.style.display !== 'none') return true
  if (document.querySelector('.t-drawer--open')) return true
  const popups = document.querySelectorAll<HTMLElement>('.t-popup')
  for (const popup of popups) {
    if (popup.style.display !== 'none') return true
  }
  return false
}

export function useModalShell(options: ModalShellOptions) {
  const { t } = useI18n()
  let cleanSnapshot = serialize(options.snapshot?.())
  let confirming = false

  const markClean = () => {
    cleanSnapshot = serialize(options.snapshot?.())
  }

  const isDirty = () => {
    if (!options.snapshot) return false
    return serialize(options.snapshot()) !== cleanSnapshot
  }

  const requestClose = () => {
    if (!options.visible() || confirming) return
    if (!isDirty()) {
      options.close()
      return
    }
    confirming = true
    const dialog = DialogPlugin.confirm({
      header: t('common.unsavedChanges.title'),
      body: t('common.unsavedChanges.body'),
      theme: 'warning',
      confirmBtn: { content: t('common.unsavedChanges.discard'), theme: 'danger' },
      cancelBtn: t('common.unsavedChanges.keepEditing'),
      onConfirm: () => {
        confirming = false
        dialog.destroy()
        options.close()
      },
      onClose: () => {
        confirming = false
        dialog.destroy()
      },
    })
  }

  const onKeydown = (event: KeyboardEvent) => {
    if (event.key !== 'Escape' || !options.visible()) return
    if (options.ignoreEscape?.()) return
    if (hasOpenTDesignOverlay()) return
    requestClose()
  }

  onMounted(() => window.addEventListener('keydown', onKeydown))
  onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

  return { requestClose, markClean, isDirty }
}
