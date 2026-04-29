import { DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

interface ConfirmDeleteOptions {
  title?: string
  body: string
  confirmText?: string
  cancelText?: string
  onConfirm: () => Promise<void> | void
}

/**
 * 统一的删除确认交互，基于 TDesign DialogPlugin.confirm。
 * 取代散落在各处的 window.confirm / t-popconfirm / 自定义 Dialog 写法。
 */
export function useConfirmDelete() {
  const { t } = useI18n()

  return (opts: ConfirmDeleteOptions) => {
    const dialog = DialogPlugin.confirm({
      header: opts.title || (t('common.confirmDelete') as string),
      body: opts.body,
      confirmBtn: opts.confirmText || (t('common.delete') as string),
      cancelBtn: opts.cancelText || (t('common.cancel') as string),
      theme: 'warning',
      onConfirm: async () => {
        try {
          await opts.onConfirm()
        } finally {
          dialog.hide()
        }
      }
    })
    return dialog
  }
}
