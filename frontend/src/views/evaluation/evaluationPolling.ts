/** Serial polling with bounded retries; stop also invalidates an in-flight refresh. */
export function createEvaluationPoller(options: {
  refresh: (isCurrent: () => boolean) => Promise<void>
  hasRunning: () => boolean
  onPaused: () => void
  schedule?: (callback: () => void, delay: number) => ReturnType<typeof setTimeout>
  cancel?: (timer: ReturnType<typeof setTimeout>) => void
}) {
  const schedule = options.schedule ?? setTimeout, cancel = options.cancel ?? clearTimeout
  let timer: ReturnType<typeof setTimeout> | undefined
  let generation = 0, failures = 0, inFlight = false
  function arm() {
    if (timer !== undefined || inFlight || !options.hasRunning()) return
    timer = schedule(() => { timer = undefined; void tick() }, failures ? Math.min(30000, 3000 * 2 ** failures) : 3000)
  }
  async function tick() {
    const token = generation; inFlight = true
    try { await options.refresh(() => token === generation); if (token === generation) failures = 0 }
    catch { if (token === generation) failures += 1 }
    finally { if (token === generation) { inFlight = false; if (failures >= 3) options.onPaused(); else arm() } }
  }
  return {
    start() { failures = 0; arm() },
    stop() { generation += 1; inFlight = false; failures = 0; if (timer !== undefined) cancel(timer); timer = undefined },
  }
}
