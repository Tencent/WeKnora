/**
 * Pure helpers for the SystemAdmin create-user dialog.
 *
 * The axios interceptor discards HTTP status, so 201 (created) and 200
 * (idempotent retry) used to look identical. The backend now sets
 * `idempotent` on the body; generated_password is still the signal for
 * the one-time reveal view.
 */

export type CreateUserIdentity = {
  username: string
  email: string
}

export type CreateUserApiResponse = {
  generated_password?: string
  idempotent?: boolean
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
  response: CreateUserApiResponse,
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
  if (response.idempotent) {
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
