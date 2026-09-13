export interface LandingRoute {
  path: string
}

/**
 * Knowledge-base list is the post-auth landing page only for auth/onboarding
 * transitions. A user navigating from the video home should stay on the
 * requested destination.
 */
export function isLegacyPostAuthLanding(to: LandingRoute, from: LandingRoute) {
  return to.path === '/platform/knowledge-bases' && (
    from.path === '/login' ||
    from.path === '/register' ||
    from.path === '/onboarding/workspace'
  )
}
