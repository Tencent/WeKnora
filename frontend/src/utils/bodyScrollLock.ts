const lockTokens = new Set<string>()
let previousOverflow = ''
let previousOverscrollBehavior = ''

const applyLockState = () => {
  if (typeof document === 'undefined') return
  const body = document.body
  const locked = lockTokens.size > 0
  if (locked && !body.classList.contains('app-scroll-locked')) {
    previousOverflow = body.style.overflow
    previousOverscrollBehavior = body.style.overscrollBehavior
    body.style.overflow = 'hidden'
    body.style.overscrollBehavior = 'none'
    body.classList.add('app-scroll-locked')
  } else if (!locked && body.classList.contains('app-scroll-locked')) {
    body.style.overflow = previousOverflow
    body.style.overscrollBehavior = previousOverscrollBehavior
    body.classList.remove('app-scroll-locked')
  }
}

export function setBodyScrollLock(token: string, locked: boolean) {
  if (locked) lockTokens.add(token)
  else lockTokens.delete(token)
  applyLockState()
}

export function releaseBodyScrollLock(token: string) {
  lockTokens.delete(token)
  applyLockState()
}
