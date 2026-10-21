// Each glyph performs one readable gesture, settles, then rests before repeating.
// The phases borrow the anticipation/settling approach of open-claude-ui's
// motion examples, implemented here with our own SVG geometry (no icon font).
export interface TimelineIconAnimation {
  attribute: 'd' | 'opacity' | 'stroke-dashoffset'
  values: string[]
  times: string
  splines: string
}

export interface TimelineIconPart {
  path: string
  animations?: TimelineIconAnimation[]
  transform?: {
    type: 'translate' | 'rotate'
    values: string[]
    times: string
    splines: string
  }
  draw?: boolean
  delay?: number
}

export const TIMELINE_ICON_CYCLE = '2.8s'
const gestureTimes = '0;.12;.3;.4;.56;.78;1'
const ease = '.22 .7 .3 1'
const settle = '.2 0 .2 1'
const gestureSplines = [ease, ease, settle, ease, settle, ease].join(';')
const traceTimes = '0;.12;.36;.6;.76;.82;1'
const traceSplines = Array(6).fill(ease).join(';')

const gesture = (rest: string, overshoot: string, target: string) => ({
  values: [rest, rest, overshoot, target, target, rest, rest],
  times: gestureTimes,
  splines: gestureSplines,
})

function move(
  part: TimelineIconPart, type: 'translate' | 'rotate',
  rest: string, overshoot: string, target: string,
): TimelineIconPart {
  return { ...part, transform: { type, ...gesture(rest, overshoot, target) } }
}

function morph(path: string, target: string, overshoot = target): TimelineIconPart {
  return { path, animations: [{ attribute: 'd', ...gesture(path, overshoot, target) }] }
}

// Draw once, hold, fade, then reset while invisible. Never erase a visible line
// backwards, flash the whole icon, or change the line weight.
function trace(path: string, delay = 0): TimelineIconPart {
  return {
    path, draw: true, delay,
    animations: [
      { attribute: 'stroke-dashoffset', values: ['1', '1', '0', '0', '0', '1', '1'], times: traceTimes, splines: traceSplines },
      { attribute: 'opacity', values: ['0', '1', '1', '1', '0', '0', '0'], times: traceTimes, splines: traceSplines },
    ],
  }
}

function reveal(path: string, delay = 0): TimelineIconPart {
  return {
    path, delay,
    animations: [{
      attribute: 'opacity', values: ['0', '0', '1', '1', '0', '0', '0'],
      times: traceTimes, splines: traceSplines,
    }],
  }
}

export function getTimelineIconMotion(name: string, paths: string[]): TimelineIconPart[] {
  const parts: TimelineIconPart[] = paths.map(path => ({ path }))
  switch (name) {
    case 'lightbulb':
    case 'ai-search':
      // Keep every lit stroke inside the glass, with space above the neck.
      // External rays can read as stray filament at the actual 18px size.
      return [...parts, trace('M9.5 9.5 12 12l2.5-2.5M12 12v2')]
    case 'search':
      return [...parts, trace('M6 10a4 4 0 0 1 4-4')]
    case 'data-search':
    case 'file-search':
      // One deliberate lens pass, followed by a small settle at the destination.
      return [parts[0], move(parts[1], 'translate', '0 0', '-2.5 -1.5', '-2 -1')]
    case 'internet':
      // Meridian bends across the globe instead of rocking the entire world.
      return [
        parts[0], parts[1],
        morph('M12 4C7 7 7 17 12 20', 'M12 4C17 7 17 17 12 20'),
        morph('M12 4C17 7 17 17 12 20', 'M12 4C7 7 7 17 12 20'),
      ]
    case 'file':
    case 'file-paste':
      return [parts[0], trace('M8 14h8'), trace('M8 17h5', .18)]
    case 'folder':
      // The front opens while the back and tab stay anchored.
      return [
        { path: 'M4 17V6a2 2 0 0 1 2-2h3l2 3h7a2 2 0 0 1 2 2v8' },
        morph(
          'M4 10h16l-1 8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z',
          'M6 11h16l-3 7a3 3 0 0 1-3 2H6a2 2 0 0 1-2-2Z',
          'M6.5 11h16l-3.5 7a3 3 0 0 1-3 2H6a2 2 0 0 1-2-2Z',
        ),
      ]
    case 'task':
      return [parts[0], parts[1], trace(parts[2].path), trace(parts[3].path, .2)]
    case 'bookmark':
      return [...parts, trace('M9 10h6'), trace('M9 13h4', .16)]
    case 'edit':
      return [move(parts[0], 'translate', '0 0', '2 -2', '1.5 -1.5'), trace('M5 21h8', .2)]
    case 'attach':
      return [move(parts[0], 'rotate', '0 12 12', '-16 12 12', '-12 12 12')]
    case 'terminal':
      return [
        move(parts[0], 'translate', '0 0', '2.5 0', '2 0'),
        trace('M15 18h5', .16),
      ]
    case 'code':
      return [
        move(parts[0], 'translate', '0 0', '-1.5 0', '-1 0'),
        move(parts[1], 'translate', '0 0', '1.5 0', '1 0'),
        trace(parts[2].path, .18),
      ]
    case 'chat':
      return [parts[0], trace('M8 10h8'), trace('M8 13h5', .18)]
    case 'agent':
      return [
        move(parts[0], 'translate', '0 0', '-1.2 0', '-.8 0'),
        { ...move(parts[1], 'translate', '0 0', '1.2 0', '.8 0'), delay: .14 },
      ]
    case 'browser':
      return [
        parts[0],
        move(parts[1], 'translate', '0 0', '-2 -1.5', '-1.5 -1'),
        reveal('M7.5 11.5h-2m3-2-1-1.5', .25),
      ]
    case 'jump':
      return [
        move(parts[0], 'translate', '0 0', '2 -2', '1.5 -1.5'),
        parts[1],
      ]
    case 'compaction':
      return [
        parts[0],
        // Keep the arrow tips apart, including at the overshoot, so their
        // strokes never collide into a heavier mark.
        morph('M8 7l4 4 4-4', 'M10 8l2 3 2-3', 'M10.5 8.2l1.5 2.8 1.5-2.8'),
        morph('M8 17l4-4 4 4', 'M10 16l2-3 2 3', 'M10.5 15.8l1.5-2.8 1.5 2.8'),
      ]
    case 'ellipsis':
      return parts.map((part, index) => ({
        ...move(part, 'translate', '0 0', '0 -2.5', '0 -2'), delay: index * .12,
      }))
    case 'secured':
    case 'check-circle':
      return [parts[0], trace(parts[1].path)]
    case 'lock-on':
      return [
        { path: 'M8 10V8a4 4 0 0 1 8 0v2' },
        { path: 'M7 10h10a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-6a2 2 0 0 1 2-2Z' },
        trace(parts[1].path),
      ]
    case 'help-circle':
      return [parts[0], trace(parts[1].path)]
    case 'time':
      return [
        parts[0], { path: 'M12 12l3 2' },
        {
          path: 'M12 12V7',
          transform: {
            type: 'rotate',
            values: [0, 0, 96, 90, 186, 180, 276, 270, 366, 360, 360].map(angle => `${angle} 12 12`),
            times: '0;.06;.18;.24;.38;.44;.58;.64;.78;.84;1',
            splines: Array(10).fill(ease).join(';'),
          },
        },
      ]
    case 'play-circle':
      return [parts[0], move(parts[1], 'translate', '0 0', '1.5 0', '1 0')]
    case 'minus-circle':
    case 'close-circle':
      return [parts[0], trace(parts[1].path)]
    case 'chevron-right':
    case 'chevron-down':
      return parts
    default:
      // Unknown tools keep the generic file glyph and its reading gesture.
      return [parts[0], trace('M8 14h8'), trace('M8 17h5', .18)]
  }
}
