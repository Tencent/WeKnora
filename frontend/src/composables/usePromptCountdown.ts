import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

export interface PromptTiming {
  requestedAt?: number
  timeoutSeconds?: number
  hotWaitSeconds?: number
  suspendable?: boolean
  suspended?: boolean
  expiresAt?: string
  resolved?: boolean
}

/**
 * Countdown of a prompt card. A suspendable prompt counts down its hot wait;
 * past it (or once suspended) it is parked: the run no longer waits in
 * memory, and answering later resumes it until expiresAt.
 */
export function usePromptCountdown(props: PromptTiming) {
  const { t, locale } = useI18n()
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null
  onMounted(() => {
    timer = setInterval(() => { now.value = Date.now() }, 1000)
  })
  onBeforeUnmount(() => {
    if (timer) clearInterval(timer)
  })

  const deadline = computed(() => {
    const base = (props.requestedAt || 0) * 1000
    const wait = props.suspendable ? props.hotWaitSeconds || props.timeoutSeconds : props.timeoutSeconds
    return base + (wait || 600) * 1000
  })

  const parked = computed(() => Boolean(props.suspended || (props.suspendable && now.value >= deadline.value)))

  const secondsLeft = computed(() => {
    if (props.resolved) return -1
    return Math.max(0, Math.floor((deadline.value - now.value) / 1000))
  })

  const timerClass = computed(() => {
    if (secondsLeft.value <= 30) return 'timer-critical'
    if (secondsLeft.value <= 120) return 'timer-warning'
    return ''
  })

  const countdownText = computed(() => {
    const s = secondsLeft.value
    if (s < 60) return t('agentStream.toolApproval.countdownShort', { seconds: s })
    return `${Math.floor(s / 60)}:${(s % 60).toString().padStart(2, '0')}`
  })

  const parkedText = computed(() => {
    const expires = props.expiresAt ? new Date(props.expiresAt) : null
    if (!expires || Number.isNaN(expires.getTime())) return t('agentStream.toolApproval.parked')
    const time = expires.toLocaleString(locale.value, {
      month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
    })
    return t('agentStream.toolApproval.parkedUntil', { time })
  })

  return { parked, secondsLeft, timerClass, countdownText, parkedText }
}
