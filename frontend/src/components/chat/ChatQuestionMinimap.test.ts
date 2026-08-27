import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./ChatQuestionMinimap.vue', import.meta.url), 'utf8')
const composable = readFileSync(new URL('../../composables/useChatQuestionMinimap.ts', import.meta.url), 'utf8')

test('does not render the rail until the thread overflows', () => {
  assert.match(component, /v-if="visible"/)
})

test('opens the question list from the left of the rail', () => {
  assert.match(component, /question-minimap__panel/)
  assert.match(component, /question-minimap__bridge/)
  assert.match(component, /CLOSE_DELAY_MS = 150/)
})

test('centers the rail and panel in the thread instead of stretching top to bottom', () => {
  assert.match(component, /top: 50%/)
  assert.match(component, /transform: translateY\(-50%\)/)
  assert.match(component, /height: `\$\{trackHeight\}px`/)
  assert.match(component, /align-items: center/)
  assert.doesNotMatch(component, /inset-block: 0/)
  assert.doesNotMatch(component, /align-self: flex-start/)
  assert.doesNotMatch(component, /height: min\(360px, 50%\)/)
  assert.match(composable, /questionMinimapTrackHeight\(measured\.length, el\.clientHeight\)/)
})

test('jumps through data-message-id via the jump event', () => {
  assert.match(component, /emit\('jump'/)
  assert.match(component, /anchoredIds/)
})

test('uses chat question minimap i18n keys', () => {
  assert.match(component, /chat\.questionMinimapTitle/)
  assert.match(component, /chat\.questionMinimapAriaLabel/)
  assert.match(component, /chat\.questionMinimapAttachmentPlaceholder/)
})

test('wraps the overlay in navigation without overriding the rail button role', () => {
  assert.match(component, /<nav[\s\S]*:aria-label="t\('chat\.questionMinimapAriaLabel'\)"/)
  assert.doesNotMatch(component, /class="question-minimap__rail"[\s\S]{0,160}role="navigation"/)
  assert.match(component, /aria-haspopup="listbox"/)
  assert.match(component, /:aria-controls="listboxId"/)
  assert.match(component, /:aria-expanded="panelOpen"/)
})

test('exposes the keyboard cursor through rail active descendant semantics', () => {
  assert.match(component, /const listboxId = /)
  assert.match(component, /const optionId = /)
  assert.match(component, /:id="listboxId"/)
  assert.match(component, /:aria-label="t\('chat\.questionMinimapTitle'\)"/)
  assert.match(component, /class="question-minimap__rail"[\s\S]{0,320}:aria-activedescendant="activeDescendantId"/)
  assert.doesNotMatch(component, /role="listbox"[\s\S]{0,160}:aria-activedescendant="activeDescendantId"/)
  assert.match(component, /:id="optionId\(question\.id\)"/)
  assert.match(component, /:aria-selected="panelOpen && index === keyboardIndex"/)
})

test('keeps scroll-driven active question changes from clobbering keyboard navigation', () => {
  assert.doesNotMatch(component, /watch\(activeId/)
})

test('does not draw a second scrollbar thumb next to the native scrollbar', () => {
  assert.doesNotMatch(component, /question-minimap__viewport/)
  assert.match(component, /RAIL_INSET_PX = 16/)
  assert.match(component, /scrollbarGutterPx \+ RAIL_INSET_PX/)
  assert.match(component, /scrollbar-width: none/)
})

test('does not paint the first question green on hover-open', () => {
  assert.match(component, /keyboardIndex\.value = -1/)
  assert.doesNotMatch(component, /question-minimap__row--active/)
  assert.doesNotMatch(component, /question-minimap__row:hover,\s*\.question-minimap__row--active/)
})

test('highlights the matching tick for active and hovered questions', () => {
  assert.match(component, /hoveredId/)
  assert.match(component, /tick\.id === activeId \|\| tick\.id === hoveredId/)
})

test('uses only approved TDesign tokens in minimap styles', () => {
  assert.doesNotMatch(component, /--td-brand-color-light/)
  assert.doesNotMatch(component, /--td-border-level-2-color/)
  assert.doesNotMatch(component, /--td-shadow-2/)
  assert.doesNotMatch(component, /--td-text-color-primary/)
})
