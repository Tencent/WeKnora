/**
 * A validator for the JSON Schema subset the harness tool registry enforces
 * (see packages/core/tools/src/json-schema.ts in DeepSeek Harness). The tests
 * run every canonical value through it, so a tool whose body drifts from its
 * declared `output.schema` fails here rather than inside dsh at runtime.
 */

const SUPPORTED_KEYS = new Set([
  'type', 'oneOf', 'properties', 'required', 'additionalProperties',
  'items', 'enum', 'const', 'description', 'title', 'default', 'examples',
])

const TYPES = new Set(['object', 'array', 'string', 'number', 'integer', 'boolean', 'null'])

/** Reject a schema the harness would refuse at registration time. */
export function assertSupportedSchema(schema, path = '#') {
  const violations = []
  const walk = (node, at) => {
    if (typeof node !== 'object' || node === null || Array.isArray(node)) {
      violations.push(`${at}: schema node must be an object`)
      return
    }
    for (const key of Object.keys(node)) {
      if (!SUPPORTED_KEYS.has(key)) violations.push(`${at}: unsupported keyword "${key}"`)
    }
    if (node.type !== undefined && !TYPES.has(node.type)) violations.push(`${at}: unsupported type "${node.type}"`)
    for (const name of node.required ?? []) {
      if (node.properties?.[name] === undefined) {
        violations.push(`${at}: required property "${name}" is not declared in properties`)
      }
    }
    for (const [name, child] of Object.entries(node.properties ?? {})) walk(child, `${at}/${name}`)
    if (node.items !== undefined) walk(node.items, `${at}/items`)
    for (const [index, branch] of (node.oneOf ?? []).entries()) walk(branch, `${at}/oneOf/${index}`)
  }
  walk(schema, path)
  if (violations.length > 0) throw new Error(`unsupported JSON schema: ${violations.join('; ')}`)
}

/** Return every way `value` fails `schema`; an empty array means it validates. */
export function validate(schema, value, path = '#') {
  const violations = []
  const walk = (node, candidate, at) => {
    if (node.const !== undefined && candidate !== node.const) {
      violations.push(`${at}: expected const ${JSON.stringify(node.const)}`)
      return
    }
    if (node.enum !== undefined && !node.enum.includes(candidate)) {
      violations.push(`${at}: ${JSON.stringify(candidate)} is not one of ${JSON.stringify(node.enum)}`)
      return
    }
    if (node.oneOf !== undefined) {
      const matches = node.oneOf.filter(branch => validate(branch, candidate, at).length === 0)
      if (matches.length !== 1) violations.push(`${at}: expected exactly one oneOf branch to match, ${matches.length} did`)
      return
    }
    switch (node.type) {
      case undefined:
        return
      case 'object': {
        if (typeof candidate !== 'object' || candidate === null || Array.isArray(candidate)) {
          violations.push(`${at}: expected object, got ${describe(candidate)}`)
          return
        }
        for (const name of node.required ?? []) {
          if (!(name in candidate)) violations.push(`${at}: missing required property "${name}"`)
        }
        if (node.additionalProperties === false) {
          for (const name of Object.keys(candidate)) {
            if (node.properties?.[name] === undefined) violations.push(`${at}: undeclared property "${name}"`)
          }
        }
        for (const [name, child] of Object.entries(node.properties ?? {})) {
          if (name in candidate) walk(child, candidate[name], `${at}/${name}`)
        }
        return
      }
      case 'array': {
        if (!Array.isArray(candidate)) {
          violations.push(`${at}: expected array, got ${describe(candidate)}`)
          return
        }
        if (node.items !== undefined) {
          candidate.forEach((entry, index) => walk(node.items, entry, `${at}/${index}`))
        }
        return
      }
      case 'integer': {
        if (typeof candidate !== 'number' || !Number.isInteger(candidate)) {
          violations.push(`${at}: expected integer, got ${describe(candidate)}`)
        }
        return
      }
      case 'number': {
        if (typeof candidate !== 'number' || !Number.isFinite(candidate)) {
          violations.push(`${at}: expected finite number, got ${describe(candidate)}`)
        }
        return
      }
      case 'string': {
        if (typeof candidate !== 'string') violations.push(`${at}: expected string, got ${describe(candidate)}`)
        return
      }
      case 'boolean': {
        if (typeof candidate !== 'boolean') violations.push(`${at}: expected boolean, got ${describe(candidate)}`)
        return
      }
      case 'null': {
        if (candidate !== null) violations.push(`${at}: expected null, got ${describe(candidate)}`)
        return
      }
      default:
        violations.push(`${at}: unsupported type "${node.type}"`)
    }
  }
  walk(schema, value, path)
  return violations
}

/** Confirm the value is representable as lossless JSON, as the registry requires. */
export function assertLosslessJson(value, label) {
  const seen = new WeakSet()
  const walk = (candidate, at) => {
    if (candidate === null || typeof candidate === 'string' || typeof candidate === 'boolean') return
    if (typeof candidate === 'number') {
      if (!Number.isFinite(candidate)) throw new Error(`${label} ${at}: ${String(candidate)} is not lossless JSON`)
      return
    }
    if (typeof candidate !== 'object') throw new Error(`${label} ${at}: ${typeof candidate} is not lossless JSON`)
    if (seen.has(candidate)) throw new Error(`${label} ${at}: circular reference`)
    seen.add(candidate)
    if (Array.isArray(candidate)) {
      candidate.forEach((entry, index) => walk(entry, `${at}/${index}`))
      return
    }
    for (const [key, entry] of Object.entries(candidate)) {
      if (entry === undefined) throw new Error(`${label} ${at}/${key}: undefined is not lossless JSON`)
      walk(entry, `${at}/${key}`)
    }
  }
  walk(value, '#')
}

function describe(value) {
  if (value === null) return 'null'
  return Array.isArray(value) ? 'array' : typeof value
}
