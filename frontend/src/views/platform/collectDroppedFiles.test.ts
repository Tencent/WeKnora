import assert from 'node:assert/strict'
import test from 'node:test'

import { collectDroppedFiles } from './collectDroppedFiles.ts'

function makeFile(name: string, content = 'x'): File {
  return new File([content], name)
}

function fileEntry(file: File) {
  return {
    isFile: true,
    isDirectory: false,
    name: file.name,
    file: (ok: (f: File) => void, _err?: (err?: unknown) => void) => {
      queueMicrotask(() => ok(file))
    },
  }
}

function dirEntry(name: string, children: unknown[], batches?: unknown[][]) {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => {
      const pages = batches ?? [children]
      let index = 0
      return {
        readEntries: (ok: (entries: unknown[]) => void) => {
          const page = index < pages.length ? pages[index] : []
          index += 1
          queueMicrotask(() => ok(page as unknown[]))
        },
      }
    },
  }
}

function phantomDirFile(name: string): File {
  return new File([], name)
}

function makeDragEvent(opts: {
  items: Array<{ kind?: string; entry?: unknown; file?: File | null; throwOnEntry?: boolean }>
  files?: File[]
}): DragEvent {
  const items = opts.items.map((item) => ({
    kind: item.kind ?? 'file',
    webkitGetAsEntry: item.throwOnEntry
      ? () => {
          throw new Error('entry failed')
        }
      : item.entry === undefined
        ? undefined
        : () => item.entry,
    getAsFile: () => item.file ?? null,
  }))
  return {
    dataTransfer: {
      items,
      files: opts.files ?? [],
    },
  } as unknown as DragEvent
}

function relativePath(file: File): string {
  return (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
}

test('folder drop enumerates files and sets webkitRelativePath', async () => {
  const nested = makeFile('day-one.md')
  const readme = makeFile('README.md')
  const event = makeDragEvent({
    items: [{
      entry: dirEntry('handbook', [
        fileEntry(readme),
        dirEntry('onboarding', [fileEntry(nested)]),
      ]),
      file: phantomDirFile('handbook'),
    }],
    files: [phantomDirFile('handbook')],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(
    files.map(f => relativePath(f) || f.name).sort(),
    ['handbook/README.md', 'handbook/onboarding/day-one.md'],
  )
})

test('empty folder does not fall back to Chrome phantom directory files', async () => {
  const phantom = phantomDirFile('empty')
  const event = makeDragEvent({
    items: [{ entry: dirEntry('empty', []), file: phantom }],
    files: [phantom],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(files, [])
})

test('hidden-only folder does not fall back to phantom directory files', async () => {
  const phantom = phantomDirFile('.git')
  const event = makeDragEvent({
    items: [{
      entry: dirEntry('.git', [fileEntry(makeFile('HEAD'))]),
      file: phantom,
    }],
    files: [phantom],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(files, [])
})

test('hidden files and directories inside a folder are skipped', async () => {
  const keep = makeFile('notes.md')
  const event = makeDragEvent({
    items: [{
      entry: dirEntry('docs', [
        fileEntry(keep),
        fileEntry(makeFile('.env')),
        dirEntry('.cache', [fileEntry(makeFile('index.json'))]),
      ]),
    }],
    files: [phantomDirFile('docs')],
  })

  const files = await collectDroppedFiles(event)
  assert.equal(files.length, 1)
  assert.equal(relativePath(files[0]), 'docs/notes.md')
})

test('top-level files keep an empty webkitRelativePath', async () => {
  const file = makeFile('report.pdf')
  const event = makeDragEvent({
    items: [{ entry: fileEntry(file), file }],
    files: [file],
  })

  const files = await collectDroppedFiles(event)
  assert.equal(files.length, 1)
  assert.equal(files[0], file)
  assert.equal(relativePath(files[0]), '')
})

test('falls back to FileList when webkitGetAsEntry is unavailable', async () => {
  const file = makeFile('report.pdf')
  const event = makeDragEvent({
    items: [{ file }],
    files: [file],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(files, [file])
})

test('mixed file and folder drops collect both', async () => {
  const loose = makeFile('loose.pdf')
  const nested = makeFile('inside.md')
  const event = makeDragEvent({
    items: [
      { entry: fileEntry(loose), file: loose },
      { entry: dirEntry('docs', [fileEntry(nested)]), file: phantomDirFile('docs') },
    ],
    files: [loose, phantomDirFile('docs')],
  })

  const files = await collectDroppedFiles(event)
  assert.equal(files.length, 2)
  assert.equal(files[0], loose)
  assert.equal(relativePath(files[0]), '')
  assert.equal(relativePath(files[1]), 'docs/inside.md')
})

test('a directory whose reader throws does not revive the phantom file', async () => {
  const keep = makeFile('keep.md')
  const badDir = {
    isFile: false,
    isDirectory: true,
    name: 'bad',
    createReader: () => {
      throw new Error('no reader')
    },
  }
  const event = makeDragEvent({
    items: [
      { entry: badDir, file: phantomDirFile('bad') },
      { entry: fileEntry(keep), file: keep },
    ],
    files: [phantomDirFile('bad'), keep],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(files, [keep])
})

test('a throwing top-level entry does not drop the rest of the batch', async () => {
  const keep = makeFile('keep.md')
  const event = makeDragEvent({
    items: [
      { throwOnEntry: true, file: null },
      { entry: fileEntry(keep), file: keep },
    ],
    files: [keep],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(files, [keep])
})

test('readEntries is drained across partial batches', async () => {
  const a = makeFile('a.md')
  const b = makeFile('b.md')
  const event = makeDragEvent({
    items: [{
      entry: dirEntry('docs', [], [[fileEntry(a)], [fileEntry(b)]]),
    }],
  })

  const files = await collectDroppedFiles(event)
  assert.deepEqual(
    files.map(f => relativePath(f)).sort(),
    ['docs/a.md', 'docs/b.md'],
  )
})
