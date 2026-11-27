import { marked, type Token } from 'marked'

export type CitationNumericConflict = {
  answer: string
  source: string
}

const QUANTITY = /[+-]?(?:\d{1,3}(?:,\d{3})+(?!\d)|\d+)(?:\.\d+)?/g
const UNIT = /^(?:%|‰|个百分点|万元|亿元|元|毫秒|秒|分钟|小时|天|日|周|月|年|人|条|项|次|份|页|个|days?|hours?|minutes?|seconds?|ms|s|GiB|MiB|GB|MB|KB)(?=$|[\s,;:，；：])/u
const MAX_SENTENCE_LENGTH = 300

type Measurement = {
  before: string
  after: string
  unit: string
  value: string
  label: string
}

type NumericStatement = {
  before: string
  following: string
  quantity: string
  multipleValues: boolean
}

function sentences(text: string): string[] {
  return text.split(/[。！？!?\n]+|\.(?=\s|$)/u).map(s => s.trim()).filter(Boolean)
}

function canonicalNumber(text: string): string {
  const [whole = '0', fraction = ''] = text.replace(/[,+-]/g, '').split('.')
  const integer = whole.replace(/^0+(?=\d)/, '')
  const decimal = fraction.replace(/0+$/, '')
  const negative = text.startsWith('-') && (integer !== '0' || decimal !== '')
  return (negative ? '-' : '') + integer + (decimal ? '.' + decimal : '')
}

function numericStatement(sentence: string): NumericStatement | null {
  if (sentence.length > MAX_SENTENCE_LENGTH || /[`<>\[\]|]/u.test(sentence)) return null
  const text = sentence.normalize('NFKC').replace(/\*\*|__/g, '').replaceAll('−', '-').replace(/\s+/g, ' ').trim()
  const numbers = [...text.matchAll(QUANTITY)]
  const number = numbers[0]
  if (!number || number.index === undefined) return null
  const before = text.slice(0, number.index).trim()
  // Bare quantities and labels like "Total" depend on surrounding context.
  if (!/(?:\p{Script=Han}{4}|[\p{L}]{2,}\s+[\p{L}]{2,})/u.test(before)) return null
  if (/[+-]$/.test(before)) return null
  if (/[\p{Script=Latin}\d_.]$/u.test(text.slice(0, number.index))) return null
  return {
    before,
    following: text.slice(number.index + number[0].length).trimStart(),
    quantity: number[0],
    multipleValues: numbers.length > 1,
  }
}

function measurement(statement: NumericStatement | null): Measurement | null {
  if (!statement || statement.multipleValues) return null
  const { before, following, quantity } = statement
  const unit = UNIT.exec(following)?.[0]
  if (!unit) return null
  return {
    before,
    after: following.slice(unit.length).trim(),
    unit: unit.replace(/^(day|hour|minute|second)s$/, '$1'),
    value: canonicalNumber(quantity),
    label: unit === '%' || unit === '‰' ? quantity + unit : `${quantity} ${unit}`,
  }
}

function sharesMetricPrefix(before: string, claimBefore: string): boolean {
  if (before === claimBefore) return true
  // Range descriptions can add words after the metric or use a label separator.
  const metric = claimBefore.replace(/(?:\s+(?:is|are|was|were)|[=:为是])$/iu, '').trim()
  if (!metric || !before.startsWith(metric)) return false
  const next = before[metric.length]
  return !next || /[\s,:(]/u.test(next) || /\p{Script=Han}$/u.test(metric)
}

function prose(tokens: Token[]): string[] {
  return tokens.flatMap(token => {
    if (token.type === 'list') return token.items.flatMap((item: { tokens: Token[] }) => prose(item.tokens))
    if (token.type === 'paragraph' || token.type === 'text') {
      const text = token.text as string
      return /[`<>]/.test(text) ? [] : [text]
    }
    return []
  })
}

/** Compare a directly cited statement with one unambiguous measurement in the recorded excerpt. */
export function findCitationNumericConflict(
  claimText: string,
  excerpt: string,
): CitationNumericConflict | null {
  const last = sentences(claimText).at(-1)
  const claim = last && measurement(numericStatement(last))
  if (!claim || !excerpt.trim()) return null

  let source: Measurement | undefined
  for (const sentence of prose(marked.lexer(excerpt)).flatMap(sentences)) {
    const statement = numericStatement(sentence)
    if (statement?.multipleValues && sharesMetricPrefix(statement.before, claim.before)) return null
    const candidate = measurement(statement)
    // Keep surrounding words as part of the match, including scope and qualifiers.
    if (!candidate || candidate.before !== claim.before || candidate.after !== claim.after ||
        candidate.unit !== claim.unit) continue
    if (source && candidate.value !== source.value) return null
    source = candidate
  }
  return source && source.value !== claim.value ? { answer: claim.label, source: source.label } : null
}
