/**
 * 极简 CSS 规则匹配器，只服务于「排版样式是否命中」这类回归测试。
 *
 * 为什么不用 jsdom / Playwright：本仓库的测试跑在 `tsx --test`（纯 Node）里，
 * 依赖里没有 DOM 实现，也不适合为了一个排版断言引入浏览器。这里用
 * `@vue/compiler-dom` 解析真实渲染出的 HTML，再用 `@vue/compiler-sfc` 的
 * `compileStyle`（与 Vite 构建同一条管线）得到编译后的 scoped CSS，最后按
 * CSS 级联规则算出目标属性的生效值。
 *
 * 只支持测试里用到的选择器子集：标签、`.class`、`[attr]`、`[attr=value]`、
 * 后代（空格）与子代（`>`）。带伪类/伪元素/兄弟选择器的规则一律视为不命中，
 * 因此不要在断言里依赖它们。
 */

import { parse as parseDom } from '@vue/compiler-dom';

export interface CssDeclaration {
  prop: string;
  value: string;
  important: boolean;
}

export interface CssRule {
  selector: string;
  declarations: CssDeclaration[];
}

export interface DomNode {
  tag: string;
  classes: string[];
  attributes: Record<string, string>;
  parent: DomNode | null;
  children: DomNode[];
}

/** 拆分选择器列表（不处理 `:has()` 等含逗号的伪类；此类规则本来就不支持）。 */
function splitSelectorList(selector: string): string[] {
  return selector
    .split(',')
    .map(part => part.trim())
    .filter(Boolean);
}

function findBlockEnd(css: string, openBrace: number): number {
  let depth = 0;
  let quote: string | null = null;
  for (let i = openBrace; i < css.length; i += 1) {
    const char = css[i];
    if (quote) {
      if (char === '\\') i += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") quote = char;
    else if (char === '{') depth += 1;
    else if (char === '}') {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return css.length - 1;
}

function splitDeclarations(body: string): string[] {
  const parts: string[] = [];
  let current = '';
  let quote: string | null = null;
  let parens = 0;
  for (const char of body) {
    if (quote) {
      current += char;
      if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") quote = char;
    else if (char === '(') parens += 1;
    else if (char === ')') parens = Math.max(0, parens - 1);
    if (char === ';' && parens === 0) {
      parts.push(current);
      current = '';
      continue;
    }
    current += char;
  }
  if (current.trim()) parts.push(current);
  return parts;
}

/** 解析编译后的 CSS 文本（跳过 `@media` 等 at-rule，测试不依赖它们）。 */
export function parseCssRules(css: string): CssRule[] {
  const source = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const rules: CssRule[] = [];
  let index = 0;
  while (index < source.length) {
    const openBrace = source.indexOf('{', index);
    if (openBrace < 0) break;
    const selector = source.slice(index, openBrace).trim();
    const end = findBlockEnd(source, openBrace);
    const body = source.slice(openBrace + 1, end);
    if (!selector.startsWith('@')) {
      rules.push({ selector, declarations: parseDeclarations(body) });
    }
    index = end + 1;
  }
  return rules;
}

function parseDeclarations(body: string): CssDeclaration[] {
  const declarations: CssDeclaration[] = [];
  for (const chunk of splitDeclarations(body)) {
    const colon = chunk.indexOf(':');
    if (colon < 0) continue;
    const prop = chunk.slice(0, colon).trim().toLowerCase();
    let value = chunk.slice(colon + 1).trim();
    let important = false;
    if (/!important\s*$/i.test(value)) {
      important = true;
      value = value.replace(/\s*!important\s*$/i, '').trim();
    }
    if (prop && value) declarations.push({ prop, value, important });
  }
  return declarations;
}

function elementProps(node: unknown): Array<Record<string, unknown>> {
  const props = (node as { props?: unknown }).props;
  return Array.isArray(props) ? (props as Array<Record<string, unknown>>) : [];
}

/** 把 `@vue/compiler-dom` 的模板 AST 转成便于匹配的简化树。 */
export function buildDomTree(html: string): DomNode {
  const root: DomNode = { tag: '#root', classes: [], attributes: {}, parent: null, children: [] };
  const ast = parseDom(html);

  const visit = (nodes: Array<Record<string, unknown>>, parent: DomNode) => {
    for (const node of nodes) {
      const type = node.type as number;
      if (type !== 1) continue; // 只关心元素节点
      const classes: string[] = [];
      const attributes: Record<string, string> = {};
      for (const prop of elementProps(node)) {
        if (prop.type !== 6) continue;
        const name = String(prop.name ?? '');
        const valueNode = prop.value as { content?: string } | undefined;
        const value = valueNode?.content ?? '';
        if (name === 'class') {
          classes.push(...value.split(/\s+/).filter(Boolean));
        } else if (name) {
          attributes[name] = value;
        }
      }
      const element: DomNode = {
        tag: String(node.tag ?? '').toLowerCase(),
        classes,
        attributes,
        parent,
        children: [],
      };
      parent.children.push(element);
      visit((node.children as Array<Record<string, unknown>>) ?? [], element);
    }
  };

  visit(
    ((ast as unknown as { children?: Array<Record<string, unknown>> }).children) ?? [],
    root,
  );
  return root;
}

function matchCompound(node: DomNode, compound: string): boolean {
  // 不支持的语法（伪类、id、通配、兄弟选择器）直接判不命中
  if (!compound || /[:#*+~]/.test(compound)) return false;
  const parsed = compound.match(/^([a-zA-Z][\w-]*)?((?:\.[\w-]+|\[[^\]]+\])*)$/);
  if (!parsed) return false;
  const [, tag, rest] = parsed;
  if (tag && node.tag !== tag.toLowerCase()) return false;

  const tokenRe = /\.([\w-]+)|\[([^\]=]+)(?:=("([^"]*)"|'([^']*)'|([^\]]+)))?\]/g;
  let match: RegExpExecArray | null;
  while ((match = tokenRe.exec(rest ?? ''))) {
    if (match[1]) {
      if (!node.classes.includes(match[1])) return false;
      continue;
    }
    const attrName = (match[2] ?? '').trim();
    const expected = match[4] ?? match[5] ?? match[6];
    if (!(attrName in node.attributes)) return false;
    if (expected !== undefined && node.attributes[attrName] !== expected) return false;
  }
  return true;
}

/** 判断元素是否命中单个选择器（支持后代与 `>` 子代组合）。 */
export function elementMatches(node: DomNode, selector: string): boolean {
  if (!selector || /[+~]/.test(selector)) return false;
  const parts = selector
    .replace(/\s*>\s*/g, ' > ')
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  if (!parts.length) return false;
  if (!matchCompound(node, parts[parts.length - 1])) return false;

  let current = node;
  let index = parts.length - 2;
  while (index >= 0) {
    const token = parts[index];
    if (token === '>') {
      const parent = current.parent;
      if (!parent || !matchCompound(parent, parts[index - 1])) return false;
      current = parent;
      index -= 2;
      continue;
    }
    let ancestor = current.parent;
    while (ancestor && !matchCompound(ancestor, token)) ancestor = ancestor.parent;
    if (!ancestor) return false;
    current = ancestor;
    index -= 1;
  }
  return true;
}

function specificity(selector: string): [number, number, number] {
  const classAndAttr = (selector.match(/\.[\w-]+|\[[^\]]+\]/g) ?? []).length;
  const tags = (selector.replace(/\[[^\]]+\]/g, ' ').match(/(^|\s|>)[a-zA-Z][\w-]*/g) ?? []).length;
  const ids = (selector.match(/#[\w-]+/g) ?? []).length;
  return [ids, classAndAttr, tags];
}

function compareSpecificity(a: [number, number, number], b: [number, number, number]): number {
  return a[0] - b[0] || a[1] - b[1] || a[2] - b[2];
}

function expand(value: string): string[] {
  return value.trim().split(/\s+/).filter(Boolean);
}

function expandLonghands(declaration: CssDeclaration): CssDeclaration[] {
  const { prop, value, important } = declaration;
  const box = (prefix: 'margin' | 'padding') => {
    const parts = expand(value);
    const [top, right = top, bottom = top, left = right] = parts;
    return [
      { prop: `${prefix}-top`, value: top, important },
      { prop: `${prefix}-right`, value: right, important },
      { prop: `${prefix}-bottom`, value: bottom, important },
      { prop: `${prefix}-left`, value: left, important },
    ];
  };
  if (prop === 'margin') return box('margin');
  if (prop === 'padding') return box('padding');
  if (prop === 'border-left' || prop === 'border-right' || prop === 'border-top' || prop === 'border-bottom') {
    const side = prop.slice('border-'.length);
    const parts = expand(value);
    const width = parts.find(part => /^(?:\d|\.)/.test(part) || part === 'thin' || part === 'medium' || part === 'thick') ?? '0';
    const style = parts.find(part => /^(?:none|hidden|dotted|dashed|solid|double|groove|ridge|inset|outset)$/.test(part)) ?? 'none';
    return [{ prop: `border-${side}-width`, value: width, important }, { prop: `border-${side}-style`, value: style, important }];
  }
  if (prop === 'border') {
    const parts = expand(value);
    const width = parts.find(part => /^(?:\d|\.)/.test(part) || /^(?:thin|medium|thick)$/.test(part)) ?? '0';
    const style = parts.find(part => /^(?:none|hidden|dotted|dashed|solid|double|groove|ridge|inset|outset)$/.test(part)) ?? 'none';
    return ['top', 'right', 'bottom', 'left'].flatMap(side => [
      { prop: `border-${side}-width`, value: width, important },
      { prop: `border-${side}-style`, value: style, important },
    ]);
  }
  if (prop === 'background') {
    return [
      { prop: 'background', value, important },
      { prop: 'background-color', value, important },
    ];
  }
  return [{ prop, value, important }];
}

/**
 * 计算某个元素上某个属性最终生效的声明（含简单级联：`!important` > 特异性 >
 * 源码顺序）。返回 `null` 表示没有任何规则命中。
 */
export function winningDeclaration(
  rules: CssRule[],
  node: DomNode,
  property: string,
): { value: string; selector: string } | null {
  let best: { value: string; selector: string; important: boolean; spec: [number, number, number] } | null = null;
  for (const rule of rules) {
    for (const selector of splitSelectorList(rule.selector)) {
      if (!elementMatches(node, selector)) continue;
      for (const declaration of rule.declarations) {
        for (const longhand of expandLonghands(declaration)) {
          if (longhand.prop !== property) continue;
          const spec = specificity(selector);
          if (
            !best ||
            (longhand.important && !best.important) ||
            (longhand.important === best.important && compareSpecificity(spec, best.spec) >= 0)
          ) {
            best = { value: longhand.value, selector, important: longhand.important, spec };
          }
        }
      }
    }
  }
  return best ? { value: best.value, selector: best.selector } : null;
}

/** 从 CSS 中找出所有命中 `node` 且声明了 `property` 的规则选择器。 */
export function matchingSelectors(rules: CssRule[], node: DomNode, property: string): string[] {
  const selectors: string[] = [];
  for (const rule of rules) {
    for (const selector of splitSelectorList(rule.selector)) {
      if (!elementMatches(node, selector)) continue;
      if (rule.declarations.some(declaration => expandLonghands(declaration).some(longhand => longhand.prop === property))) {
        selectors.push(selector);
      }
    }
  }
  return selectors;
}
