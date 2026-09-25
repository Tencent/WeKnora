/**
 * Time axis for the knowledge-processing waterfall.
 *
 * The timeline used to divide the raw trace duration into four equal
 * parts, so a 494ms trace was ruled 0 / 124 / 247 / 371 / 494ms. Those
 * are exact and useless: you cannot eyeball a bar against them, and the
 * numbers change on every poll while a trace is live.
 *
 * Instead, round the tick interval to a 1 / 2 / 2.5 / 5 x 10^n step and
 * extend the axis maximum up to a whole number of steps. Bars are then
 * positioned against `maxMs` — never the raw total — so what the bars
 * show and what the ruler claims always agree.
 */

export interface AxisTick {
  left: string
  label: string
}

export interface TraceAxis {
  stepMs: number
  maxMs: number
}

const DEFAULT_TARGET_INTERVALS = 5
const MAX_TICKS = 12

/** Round a rough interval up to the nearest 1 / 2 / 2.5 / 5 x 10^n. */
export function niceAxisStep(roughStep: number): number {
  if (!Number.isFinite(roughStep) || roughStep <= 0) return 1
  const base = Math.pow(10, Math.floor(Math.log10(roughStep)))
  const n = roughStep / base
  const mult = n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10
  return mult * base
}

export function computeTraceAxis(
  totalMs: number,
  targetIntervals = DEFAULT_TARGET_INTERVALS,
): TraceAxis {
  if (!Number.isFinite(totalMs) || totalMs <= 0) return { stepMs: 0, maxMs: 0 }
  const stepMs = niceAxisStep(totalMs / targetIntervals)
  return { stepMs, maxMs: Math.max(stepMs, Math.ceil(totalMs / stepMs) * stepMs) }
}

/**
 * One unit for the whole ruler, chosen from the axis maximum. Per-value
 * formatting would print "0ms / 10.00s / 20.00s" on a single axis.
 */
export function formatAxisLabel(ms: number, maxMs: number): string {
  if (maxMs >= 60000) {
    let mins = Math.floor(ms / 60000)
    let secs = Math.round((ms % 60000) / 1000)
    // Carry, so 119_999ms reads 2m00s rather than 1m60s.
    if (secs >= 60) {
      mins += 1
      secs -= 60
    }
    return `${mins}m${String(secs).padStart(2, '0')}s`
  }
  if (maxMs >= 1000) {
    const secs = ms / 1000
    return `${Number.isInteger(secs) ? secs : secs.toFixed(1)}s`
  }
  return `${Math.round(ms)}ms`
}

export function buildAxisTicks(axis: TraceAxis): AxisTick[] {
  const { stepMs, maxMs } = axis
  if (!stepMs || !maxMs) return []
  const ticks: AxisTick[] = []
  // `stepMs / 1000` absorbs float drift so the closing tick isn't dropped.
  for (let v = 0; v <= maxMs + stepMs / 1000 && ticks.length < MAX_TICKS; v += stepMs) {
    ticks.push({ left: `${(v / maxMs) * 100}%`, label: formatAxisLabel(v, maxMs) })
  }
  return ticks
}

/** Gap between gridlines, as a CSS percentage of the bar track. */
export function axisGridStepPct(axis: TraceAxis): string {
  if (!axis.maxMs || !axis.stepMs) return '25%'
  return `${(axis.stepMs / axis.maxMs) * 100}%`
}
