import { reactive } from 'vue'

export type ToastKind = 'ok' | 'error' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  text: string
}

interface DialogState {
  title: string
  message: string
  confirmText: string
  cancelText: string
  danger: boolean
  resolve: (value: boolean) => void
}

export const ui = reactive({
  toasts: [] as ToastItem[],
  dialog: null as DialogState | null
})

let seq = 0

export function toast(text: string, kind: ToastKind = 'ok'): void {
  const id = ++seq
  ui.toasts.push({ id, kind, text })
  window.setTimeout(() => {
    const idx = ui.toasts.findIndex((t) => t.id === id)
    if (idx >= 0) ui.toasts.splice(idx, 1)
  }, 3200)
}

export interface ConfirmOptions {
  title: string
  message?: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

export function confirmDialog(opts: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    ui.dialog = {
      title: opts.title,
      message: opts.message ?? '',
      confirmText: opts.confirmText ?? '确认',
      cancelText: opts.cancelText ?? '取消',
      danger: opts.danger ?? false,
      resolve
    }
  })
}

export function settleDialog(value: boolean): void {
  const dialog = ui.dialog
  ui.dialog = null
  dialog?.resolve(value)
}

/** 统一错误文案。 */
export function errText(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  return '操作失败，请稍后重试'
}
