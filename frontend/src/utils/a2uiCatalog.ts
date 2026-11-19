import type { Catalog } from 'a2ui-vue'

// The three values our own Button needs, all read from the protocol node, never shared with the implementation as a constant.
const VARIANTS = ['default', 'primary', 'borderless'] as const
type Variant = (typeof VARIANTS)[number]

// variant → class. Written as a pure function so it can be asserted directly in node (this repo has no jsdom,
// component rendering is untestable; the class-name mapping is the one spot here that would "fail ugly": a wrong concatenation = a button with no styles).
//
// Why not just concatenate the variant into the class: its value comes from the model's fenced block. The protocol schema is only
// validated on the agent side, and the renderer is tolerant of types with an unregistered schema, so treat it here as **untrusted input**:
// if it is not in the whitelist, fall back to default. Concatenating directly would let variant:"primary a2ui-btn--x" inject our own class names into the DOM.
export function a2uiButtonClass(variant: unknown): string {
  const v = (VARIANTS as readonly string[]).includes(String(variant))
    ? (String(variant) as Variant)
    : 'default'
  return `a2ui-btn a2ui-btn--${v}`
}

// a2uiTextFieldInputType maps the official TextField variant onto an HTML
// input type; anything outside the published enum stays a plain text box.
const A2UI_INPUT_TYPES = new Map<string, string>([
  ['number', 'number'],
  ['obscured', 'password'],
])

export function a2uiTextFieldInputType(variant: unknown): string {
  return A2UI_INPUT_TYPES.get(String(variant)) ?? 'text'
}


// Override the Button in the official basic catalog; every other component is left to a2ui-vue unchanged.
//
// It must be a **function** taking the base catalog as a parameter: that way this module never statically imports 'a2ui-vue'.
// A static import would pull those 400 KB into the entry bundle, and a lazy-load acceptance check asserts the entry bundle
// never contains the data-a2ui-frames / a2ui.org/specification/v0_9 markers — lazy loading
// is a hard constraint, not a performance fine-tuning.
//
// Why we must write our own Button: the official Vue renderer **never reads variant**.
// In A2UIButton's compiled output `variant` appears exactly once (the props declaration) and the template references it zero times, so
// primary / default / borderless all render identically; and the <button> it renders
// has no type=, so embedding it in any <form> would submit the form on click (which is the current behavior).
// Both gaps are things our own component can close on the side.
export function weknoraCatalog(base: Catalog): Catalog {
  return {
    ...base,
    Button: {
      // Dynamic import: Button is only pulled in when a card actually appears, on the same lazy-load graph as A2uiCard.vue.
      // .then(m => m.default) matches how the library's own DEFAULT_CATALOG is written (CatalogLoader
      // declares its return type as Promise<Component>, not a module namespace).
      type: () => import('@/views/chat/components/a2ui/A2uiButton.vue').then((m) => m.default),
      props: (node) => ({
        action: node.properties?.action,
        variant: node.properties?.variant,
      }),
    },
    TextField: {
      type: () => import('@/views/chat/components/a2ui/A2uiTextField.vue').then((m) => m.default),
      props: (node) => ({
        variant: node.properties?.variant,
      }),
    },
  }
}
