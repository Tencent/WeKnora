import assert from 'node:assert/strict'
import test from 'node:test'
import { continueListOnEnter, countContent, indentOnTab } from './markdownEditing'

const caret = (value: string, at: number) => ({ value, start: at, end: at })

test('Enter continues a bullet, a task item and a numbered list', () => {
  const bullet = continueListOnEnter(caret('- first', 7))
  assert.deepEqual(bullet, { value: '- first\n- ', start: 10, end: 10 })

  const task = continueListOnEnter(caret('- [x] done', 10))
  assert.deepEqual(task, { value: '- [x] done\n- [ ] ', start: 17, end: 17 })

  const ordered = continueListOnEnter(caret('  9. nine', 9))
  assert.deepEqual(ordered, { value: '  9. nine\n  10. ', start: 16, end: 16 })
})

test('Enter on an empty item ends the list instead of stacking markers', () => {
  const patch = continueListOnEnter(caret('- first\n- ', 10))
  assert.deepEqual(patch, { value: '- first\n', start: 8, end: 8 })
})

for (const { value, at, expected } of [
  { value: '- hello', at: 2, expected: '- \n- hello' },
  { value: '* hello', at: 2, expected: '* \n* hello' },
  { value: '1. hello', at: 3, expected: '1. \n2. hello' },
  { value: '- [x] hello', at: 6, expected: '- [x] \n- [ ] hello' },
  { value: '  - hello', at: 4, expected: '  - \n  - hello' },
  { value: 'before\n- hello\nafter', at: 9, expected: 'before\n- \n- hello\nafter' },
]) {
  test(`Enter before existing list content preserves the marker: ${JSON.stringify(value)}`, () => {
    const patch = continueListOnEnter(caret(value, at))
    assert.ok(patch)
    assert.equal(patch.value, expected)
    assert.equal(patch.start, expected.indexOf('hello'))
    assert.equal(patch.end, patch.start)
  })
}

test('Enter in the middle of list content splits the item', () => {
  assert.deepEqual(continueListOnEnter(caret('- hello world', 7)), {
    value: '- hello\n-  world', start: 10, end: 10,
  })
})

test('an empty item with trailing whitespace still exits the list', () => {
  assert.deepEqual(continueListOnEnter(caret('- first\n-   ', 10)), {
    value: '- first\n  ', start: 8, end: 8,
  })
})

test('Enter outside a list and over a selection is left to the browser', () => {
  assert.equal(continueListOnEnter(caret('plain text', 10)), null)
  assert.equal(continueListOnEnter({ value: '- first', start: 2, end: 7 }), null)
})

for (const { name, before, item } of [
  { name: 'backtick fence', before: '```sh', item: '- literal' },
  { name: 'tilde fence', before: '~~~text', item: '1. literal' },
  { name: 'empty task inside a fence', before: '```markdown', item: '- [ ] ' },
  { name: 'indented fence', before: '   ```yaml', item: '  - literal' },
  { name: 'shorter delimiter', before: '````md\n```', item: '- literal' },
  { name: 'different delimiter', before: '```md\n~~~', item: '- literal' },
  { name: 'delimiter with trailing text', before: '```md\n```not-a-close', item: '- literal' },
  { name: 'CRLF fence', before: '```sh\r', item: '- literal' },
]) {
  test(`Enter preserves literal list text inside a ${name}`, () => {
    const value = `${before}\n${item}`
    assert.equal(continueListOnEnter(caret(value, value.length)), null)
  })
}

for (const before of [
  '```sh\n- code\n```',
  '~~~text\n1. code\n~~~~  ',
  '   ````md\n- code\n   `````',
  '```sh\r\n- code\r\n```\r',
  '    ```not-a-fence',
  '```invalid`info',
  '`inline code`',
]) {
  test(`Enter continues a real list after ${JSON.stringify(before)}`, () => {
    const value = `${before}\n- item`
    assert.deepEqual(continueListOnEnter(caret(value, value.length)), {
      value: `${value}\n- `, start: value.length + 3, end: value.length + 3,
    })
  })
}

test('Enter inside a closed code block checks only lines before the caret', () => {
  const value = '```sh\n- literal\n```\n- real list'
  const at = value.indexOf('\n```', 6)
  assert.equal(continueListOnEnter(caret(value, at)), null)
})

test('Tab indents a list line and Shift+Tab takes it back out', () => {
  const indented = indentOnTab(caret('- item', 6), false)
  assert.deepEqual(indented, { value: '  - item', start: 8, end: 8 })

  const outdented = indentOnTab(caret('  - item', 8), true)
  assert.deepEqual(outdented, { value: '- item', start: 6, end: 6 })
})

for (const { name, value, at, expectedValue, expectedAt } of [
  { name: 'inside the item', value: '  - item', at: 6, expectedValue: '- item', expectedAt: 4 },
  { name: 'at the line start', value: '  - item', at: 0, expectedValue: '- item', expectedAt: 0 },
  { name: 'inside removed indentation', value: '  - item', at: 1, expectedValue: '- item', expectedAt: 0 },
  { name: 'after removed indentation', value: '  - item', at: 2, expectedValue: '- item', expectedAt: 0 },
  { name: 'with one space', value: ' - item', at: 7, expectedValue: '- item', expectedAt: 6 },
  { name: 'with a tab', value: '\t- item', at: 7, expectedValue: '- item', expectedAt: 6 },
  { name: 'with deeper indentation', value: '    - item', at: 10, expectedValue: '  - item', expectedAt: 8 },
  { name: 'without indentation', value: '- item', at: 6, expectedValue: '- item', expectedAt: 6 },
  { name: 'after another line', value: 'before\n  - item\nafter', at: 8, expectedValue: 'before\n- item\nafter', expectedAt: 7 },
]) {
  test(`Shift+Tab keeps a collapsed caret ${name}`, () => {
    assert.deepEqual(indentOnTab(caret(value, at), true), {
      value: expectedValue,
      start: expectedAt,
      end: expectedAt,
    })
  })
}

test('typing after outdenting inserts at the caret instead of replacing the item', () => {
  const patch = indentOnTab(caret('  - item', 8), true)
  assert.ok(patch)
  const typed = patch.value.slice(0, patch.start) + ' next' + patch.value.slice(patch.end)
  assert.equal(typed, '- item next')
})

test('Shift+Tab keeps explicitly selected lines selected', () => {
  assert.deepEqual(indentOnTab({ value: '  - item', start: 4, end: 6 }, true), {
    value: '- item', start: 0, end: 6,
  })
  assert.deepEqual(indentOnTab({ value: '  - first\n\t- second', start: 2, end: 19 }, true), {
    value: '- first\n- second', start: 0, end: 16,
  })
})

test('Tab continues to preserve a caret or selection within a list item', () => {
  assert.deepEqual(indentOnTab(caret('- item', 4), false), {
    value: '  - item', start: 6, end: 6,
  })
  assert.deepEqual(indentOnTab({ value: '- item', start: 2, end: 4 }, false), {
    value: '  - item', start: 4, end: 6,
  })
})

test('Tab indents every line of a multi-line selection but skips blank ones', () => {
  const patch = indentOnTab({ value: 'a\n\nb', start: 0, end: 4 }, false)
  assert.equal(patch?.value, '  a\n\n  b')
  assert.deepEqual([patch?.start, patch?.end], [0, 8])
})

for (const outdent of [false, true]) {
  for (const followingLine of ['- second', '']) {
    test(`${outdent ? 'Shift+Tab' : 'Tab'} excludes a line whose start is the selection end (${followingLine ? 'next item' : 'empty final line'})`, () => {
      const first = outdent ? '  - first' : '- first'
      const next = outdent && followingLine ? '  ' + followingLine : followingLine
      const changed = outdent ? '- first' : '  - first'
      const value = `${first}\n${next}`
      const patch = indentOnTab({ value, start: 0, end: first.length + 1 }, outdent)
      assert.deepEqual(patch, {
        value: `${changed}\n${next}`,
        start: 0,
        end: changed.length + 1,
      })
      // Keep the trailing newline selected so repeated Tab works on the same block.
      assert.ok(patch)
      const repeated = indentOnTab(patch, outdent)
      assert.ok(repeated)
      assert.equal(repeated.value.slice(repeated.end), next)
    })
  }
}

test('a selection ending within the following line still indents that line', () => {
  assert.deepEqual(indentOnTab({ value: '- first\n- second', start: 0, end: 9 }, false), {
    value: '  - first\n  - second', start: 0, end: 20,
  })
})

test('Tab preserves a newline-ended selection of plain text for repeated indentation', () => {
  const patch = indentOnTab({ value: 'first\nsecond\nthird', start: 2, end: 13 }, false)
  assert.deepEqual(patch, {
    value: '  first\n  second\nthird', start: 0, end: 17,
  })
  assert.ok(patch)
  assert.deepEqual(indentOnTab(patch, false), {
    value: '    first\n    second\nthird', start: 0, end: 21,
  })
})

test('Tab stays a focus move when it would not indent anything', () => {
  assert.equal(indentOnTab(caret('plain text', 4), false), null)
})

test('countContent reports characters and lines', () => {
  assert.deepEqual(countContent('one\ntwo'), { characters: 7, lines: 2 })
  assert.deepEqual(countContent(''), { characters: 0, lines: 0 })
})

for (const { name, opening, body, closing } of [
  { name: 'bullet', opening: '- ```md', body: '  - literal', closing: '  ```' },
  { name: 'ordered', opening: '10. ~~~md', body: '    1. literal', closing: '    ~~~' },
  { name: 'nested', opening: '- outer\n  - ```md', body: '    - literal', closing: '    ```' },
  { name: 'tabbed nested', opening: '- outer\n\t- ```md', body: '\t  - literal', closing: '\t  ```' },
  { name: 'continuation', opening: '- outer\n  ```md', body: '  - literal', closing: '  ```' },
  { name: 'literal list-prefixed delimiter', opening: '- ```md\n  - ```', body: '  - literal', closing: '  ```' },
]) {
  test(`Enter respects a fence in a ${name} list container`, () => {
    const inside = `${opening}\n${body}`
    assert.equal(continueListOnEnter(caret(inside, inside.length)), null)
    const after = `${inside}\n${closing}\n- next`
    assert.deepEqual(continueListOnEnter(caret(after, after.length)), {
      value: `${after}\n- `, start: after.length + 3, end: after.length + 3,
    })
  })
}

test('leaving a list container ends its unclosed fence', () => {
  const value = '- ```md\n  - literal\n- next'
  assert.deepEqual(continueListOnEnter(caret(value, value.length)), {
    value: `${value}\n- `, start: value.length + 3, end: value.length + 3,
  })
})

test('Enter inside a closed list-prefixed fence checks only lines before the caret', () => {
  const value = '- ```md\n  - literal\n  ```\n- next'
  const at = value.indexOf('\n  ```')
  assert.equal(continueListOnEnter(caret(value, at)), null)
})
