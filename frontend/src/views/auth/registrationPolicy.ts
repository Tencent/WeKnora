/** Whether the login page may offer password registration. */
export function canRegister(mode: string, hasValidInvitation: boolean): boolean {
  // register-by-invite is a dedicated endpoint that accepts a valid share-link
  // token regardless of the public registration mode. invite_only must still
  // let an invited user without an account create one on that path, otherwise
  // the share link dead-ends on the login card.
  return (
    mode === 'self_serve' ||
    ((mode === 'invite_register' || mode === 'invite_only') && hasValidInvitation)
  )
}
