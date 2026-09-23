import type {
  ImageAttrCondition,
  ImageAttrSchema,
  ImageAttrSpec,
} from '@/api/knowledge-base'

// ---------------------------------------------------------------------------
// Display text for the image-attribute panel.
//
// The registry returned by GET /image-attrs/schema is the single source of
// truth — it carries the attribute's name, what it measures, and what each of
// its values means. That is what lets a non-technical operator read
// "Text in the image: a block of body text" instead of
// "contain.text: block", and it is why adding an attribute to the backend shows
// up here with no frontend change.
//
// Translations are an overlay keyed by the attribute name, applied on top of
// the registry text. Anything not translated yet falls back to the registry
// wording, so a brand-new attribute still reads as words rather than as a bare
// key. (The i18n keys are built at runtime from the attribute name, which is
// why the `imageAttr.` prefix is registered in localeKeyAudit's EXTRA_PREFIXES.)
// ---------------------------------------------------------------------------

type Translate = (key: string) => string
type HasTranslation = (key: string) => boolean

/** One allowed value of an attribute, with its meaning in words. */
export interface ImageAttrValueDisplay {
  /** The raw value as it appears in the registry and the processing trace. */
  value: string
  /** The same value in words. */
  label: string
}

/** One attribute as the settings panel shows it. */
export interface ImageAttrDisplay {
  /** Stable attribute key, e.g. "contain.text". Shown small, to line up with the trace. */
  name: string
  /** Human-readable name, e.g. "Text in the image". */
  label: string
  /** One line saying what the attribute measures. Empty when unavailable. */
  description: string
  /** Every allowed value with its meaning, in the registry's order. */
  values: ImageAttrValueDisplay[]
}

/** One OCR policy condition, in words plus the raw pair. */
export interface ImageAttrConditionDisplay {
  /** e.g. "Text in the image = a block of body text" */
  label: string
  /** e.g. "contain.text = block" — keeps the panel aligned with the trace. */
  raw: string
}

/** The values a spec declares; presence attributes list true/false implicitly. */
function specValues(attr: ImageAttrSpec): string[] {
  if (attr.values && attr.values.length) return attr.values
  return attr.type === 'presence' ? ['true', 'false'] : []
}

/**
 * The i18n namespace for one attribute.
 *
 * vue-i18n resolves a key by walking it segment by segment on the dots, so a
 * literal `'contain.text'` message key is unreachable through
 * `imageAttr.contain.text.label` — the lookup goes looking for `imageAttr` →
 * `contain` → `text` and stops. Attribute names therefore escape their dots
 * (`contain.text` → `contain_text`) to become a single resolvable segment. The
 * same mapping is applied in every locale bundle, so a new attribute only needs
 * its name spelled the same way.
 */
export function imageAttrKeyBase(name: string): string {
  return `imageAttr.${name.replace(/\./g, '_')}`
}

/** Look up `key`, falling back to the registry's own wording. */
function pick(key: string, fallback: string, t: Translate, te: HasTranslation): string {
  return te(key) ? t(key) : fallback
}

/**
 * Render one registry attribute for the settings panel. Never returns an empty
 * label: an attribute that arrives without display text still shows its name,
 * so the panel degrades to identifiers rather than to blanks.
 */
export function imageAttrDisplay(
  attr: ImageAttrSpec,
  t: Translate,
  te: HasTranslation,
): ImageAttrDisplay {
  const base = imageAttrKeyBase(attr.name)
  return {
    name: attr.name,
    label: pick(`${base}.label`, attr.label || attr.name, t, te),
    description: pick(`${base}.description`, attr.description ?? '', t, te),
    values: specValues(attr).map((value) => ({
      value,
      label: pick(`${base}.values.${value}`, attr.value_labels?.[value] ?? value, t, te),
    })),
  }
}

/**
 * Render one OCR policy condition in the same words as the attribute list,
 * keeping the raw `prop = value` pair so an operator can still match the panel
 * against the processing trace.
 */
export function imageAttrConditionDisplay(
  cond: ImageAttrCondition,
  schema: ImageAttrSchema,
  t: Translate,
  te: HasTranslation,
): ImageAttrConditionDisplay {
  const raw = `${cond.prop} = ${cond.is}`
  const attr = schema.attributes.find((candidate) => candidate.name === cond.prop)
  if (!attr) return { label: raw, raw }

  const display = imageAttrDisplay(attr, t, te)
  const value = display.values.find((candidate) => candidate.value === cond.is)
  return { label: `${display.label} = ${value ? value.label : cond.is}`, raw }
}
