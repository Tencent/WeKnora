import ts from 'typescript'

export interface DuplicateLocaleProperty {
  path: string
  firstLine: number
  duplicateLine: number
}

/** Inspect source objects before JavaScript evaluation can overwrite duplicate keys. */
export function findDuplicateLocaleProperties(source: string, filename = 'locale.ts'): DuplicateLocaleProperty[] {
  const file = ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const duplicates: DuplicateLocaleProperty[] = []
  const nameOf = (name: ts.PropertyName): string | undefined => {
    if (ts.isIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name)) return name.text
    if (ts.isComputedPropertyName(name)) {
      const expression = name.expression
      if (ts.isStringLiteral(expression) || ts.isNumericLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) return expression.text
    }
    return undefined
  }
  const visit = (node: ts.Node, path: string) => {
    if (ts.isObjectLiteralExpression(node)) {
      const seen = new Map<string, number>()
      for (const property of node.properties) {
        const name = property.name && nameOf(property.name)
        const childPath = name === undefined ? path : path ? `${path}.${name}` : name
        if (name !== undefined) {
          const line = file.getLineAndCharacterOfPosition(property.getStart(file)).line + 1
          const firstLine = seen.get(name)
          if (firstLine !== undefined) duplicates.push({ path: childPath, firstLine, duplicateLine: line })
          else seen.set(name, line)
        }
        ts.forEachChild(property, child => visit(child, childPath))
      }
    } else if (ts.isArrayLiteralExpression(node)) {
      node.elements.forEach((element, index) => visit(element, `${path}[${index}]`))
    } else {
      ts.forEachChild(node, child => visit(child, path))
    }
  }
  visit(file, '')
  return duplicates
}
