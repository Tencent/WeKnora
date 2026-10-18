/**
 * Whether the login page may offer password registration.
 *
 * `firstRegistrationOpen` mirrors the backend's fresh-deployment bypass
 * (issue #1866): a deployment with zero accounts keeps registration open
 * even while registration_mode is closed, so the operator can create the
 * initial account and complete the documented bootstrap flow. The flag
 * comes from GET /auth/config and stays in lock-step with the API gate.
 */
export function canRegister(
  mode: string,
  hasValidInvitation: boolean,
  firstRegistrationOpen = false,
): boolean {
  return (
    mode === 'self_serve' ||
    firstRegistrationOpen ||
    (mode === 'invite_register' && hasValidInvitation)
  )
}
