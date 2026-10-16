/** Whether the login page may offer password registration. */
export function canRegister(mode: string, hasValidInvitation: boolean): boolean {
  return mode === 'self_serve' || (mode === 'invite_register' && hasValidInvitation)
}
