/**
 * Pure helpers for the SystemAdmin create-user dialog.
 */

import type { CreateSystemUserResult } from "@/api/system"

export type CreateUserIdentity = {
  username: string
  email: string
}

export type CreateUserReveal = CreateUserIdentity & {
  generatedPassword: string
}

export type CreateUserView =
  | ({ kind: 'reveal' } & CreateUserReveal)
  | { kind: 'created' }
  | { kind: 'idempotent' }
  | { kind: 'missingPassword' }

export type CreateUserNotice = 'reveal' | 'created' | 'idempotent' | 'missingPassword'

export function resolveCreateUserView(
  response: Omit<CreateSystemUserResult, 'user'>,
  identity: CreateUserIdentity,
  autoGenerate: boolean,
): CreateUserView {
  const generated = response.generated_password ?? ''
  if (generated) {
    return {
      kind: 'reveal',
      username: identity.username,
      email: identity.email,
      generatedPassword: generated,
    }
  }
  if (!response.created) {
    return { kind: 'idempotent' }
  }
  if (autoGenerate) {
    return { kind: 'missingPassword' }
  }
  return { kind: 'created' }
}

export function applyCreateUserResponse(
  current: CreateUserReveal | null,
  view: CreateUserView,
): { success: CreateUserReveal | null; close: boolean; notice: CreateUserNotice | null } {
  // A later 200/500-shaped success must not dismiss a one-time password
  // that is already on screen (double-submit race).
  if (current) {
    return { success: current, close: false, notice: null }
  }
  switch (view.kind) {
    case 'reveal':
      return {
        success: {
          username: view.username,
          email: view.email,
          generatedPassword: view.generatedPassword,
        },
        close: false,
        notice: 'reveal',
      }
    case 'created':
      return { success: null, close: true, notice: 'created' }
    case 'idempotent':
      return { success: null, close: true, notice: 'idempotent' }
    case 'missingPassword':
      return { success: null, close: false, notice: 'missingPassword' }
  }
}

export function shouldAcceptCreateUserSubmit(submitting: boolean, revealing: boolean): boolean {
  return !submitting && !revealing
}

export function captureLockedEscape(
  dialogVisible: boolean,
  locked: boolean,
  event: { key?: string; code?: string; stopImmediatePropagation: () => void },
): boolean {
  if (!dialogVisible || !locked) return false
  if (event.key !== 'Escape' && event.code !== 'Escape') return false
  event.stopImmediatePropagation()
  return true
}

export function formatCreateUserCredentials(
  reveal: CreateUserReveal,
  labels: { username: string; email: string; password: string },
): string {
  return [
    `${labels.username}: ${reveal.username}`,
    `${labels.email}: ${reveal.email}`,
    `${labels.password}: ${reveal.generatedPassword}`,
  ].join('\n')
}
