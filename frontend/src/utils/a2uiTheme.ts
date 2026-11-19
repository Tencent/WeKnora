import type { A2UITheme } from 'a2ui-vue'

// A2UI card theme overrides.
//
// Why this file exists: a2ui-vue's defaultTheme.additionalStyles.* are **inline styles**,
// bound straight onto the style attribute of <button> / <h*> / Card by the component templates
// (that line in the compiled output: `style: h(m(s).additionalStyles?.Button)`), which outranks any stylesheet — so
// the only way to tame this look inside our own UI is to override it from the same entry point, right here.
//
// The override goes through the official A2UITheme shape. Note that provideA2UI({ theme }) **stores the theme verbatim**,
// it does not merge with defaultTheme (the compiled output is literally `theme: e.theme`), so whatever must be kept
// is spread from base and inherited, replacing only the items pointed out below.
//
// Every item here is "blanked out", not "swapped for another color scheme": recoloring is the job of A2uiButton.vue (the buttons) and
// assets/theme/a2ui-card.css (card background, color-scale tokens) — one layer per concern, don't repeat it here.

// The style blocks bound straight onto elements:
//   Button —— purple gradient + padding:12px 28px + text-transform:uppercase.
//             Button is already taken over by our own component (see a2uiCatalog.ts), so nothing reads this entry anymore;
//             blanking it anyway is deliberate: leaving an unread purple gradient behind would have the next person tuning colors
//             against it for ages, and if the catalog override ever breaks, the fallback is a plain button (obvious at a glance),
//             not a purple gradient (which would masquerade as "the styles took effect").
//   Card   —— the radial purple glow on the card layer. The card sits inside a chat bubble that already has a background,
//             so layering another glow makes it "a picture inside a picture". Border and padding are not here (they live in
//             components.Card's utility classes), so clearing this entry does not affect how the card takes shape.
const STYLE_BLOCKS = ['Button', 'Card'] as const

// Maps holding one style block per variant: Text's h1/h2/h3 carry that "gradient text" look
// (color:transparent + background-clip:text + -webkit-text-fill-color:transparent);
// the moment the background stops painting (printing, some reduced-motion scenarios), the heading disappears entirely.
// This group **keeps the variant keys and only clears each variant's styles**: written as "iterate over base's keys"
// instead of "hand-write the seven h1…caption entries", because the day the library adds a variant (h6, code…)
// a hand-written list would miss it — and a missed variant would come back wearing the gradient, the exact thing we are trying to eliminate.
const VARIANT_MAPS = ['Text'] as const

export function weknoraTheme(base: A2UITheme): A2UITheme {
  const additionalStyles: Record<string, unknown> = { ...base.additionalStyles }

  for (const name of STYLE_BLOCKS) {
    if (name in additionalStyles) additionalStyles[name] = {}
  }

  for (const name of VARIANT_MAPS) {
    const block = additionalStyles[name]
    if (!block || typeof block !== 'object') continue
    const blanked: Record<string, Record<string, never>> = {}
    for (const variant of Object.keys(block as Record<string, unknown>)) blanked[variant] = {}
    additionalStyles[name] = blanked
  }

  return { ...base, additionalStyles }
}
