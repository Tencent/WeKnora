import type { FormRule } from 'tdesign-vue-next'

export const PASSWORD_SPECIAL_CHARS = '!@#$%^&*()_+-=[]{}|;:,.<>?'
export const PASSWORD_SPECIAL_CHAR_REGEX = /[!@#$%^&*()_+\-=\[\]{}|;:,.<>?]/

type Translate = (key: string, params?: Record<string, unknown>) => string

export function newPasswordRules(
  t: Translate,
  complexEnabled: boolean,
  extra: FormRule[] = [],
): FormRule[] {
  const common: FormRule[] = [
    { required: true, message: t('auth.passwordRequired'), type: 'error' },
    { min: 8, message: t('auth.passwordMinLength'), type: 'error' },
    { max: 32, message: t('auth.passwordMaxLength'), type: 'error' },
  ]
  if (complexEnabled) {
    return [
      ...common,
      { pattern: /[a-z]/, message: t('auth.passwordMustContainLowercaseLetter'), type: 'error' },
      { pattern: /[A-Z]/, message: t('auth.passwordMustContainUppercaseLetter'), type: 'error' },
      { pattern: /\d/, message: t('auth.passwordMustContainNumber'), type: 'error' },
      {
        pattern: PASSWORD_SPECIAL_CHAR_REGEX,
        message: t('auth.passwordMustContainSpecialChar', { specialChars: PASSWORD_SPECIAL_CHARS }),
        type: 'error',
      },
      ...extra,
    ]
  }
  return [
    ...common,
    { pattern: /[a-zA-Z]/, message: t('auth.passwordMustContainLetter'), type: 'error' },
    { pattern: /\d/, message: t('auth.passwordMustContainNumber'), type: 'error' },
    ...extra,
  ]
}
