<script setup lang="ts">
import { citationAnchorText } from '../../src/utils/citationAnchor'
import { sourceImageDigest } from '../../src/utils/sourceImage'
import { nextTick, onMounted, ref } from 'vue'
import PdfSourceViewer, { type PdfLocateResult } from '../../src/components/source-preview/PdfSourceViewer.vue'
import { sourceQuoteText, type SourceLocateRequest } from '../../src/utils/sourceLocator'
import { fixturePdf } from './pdfFixture'
import parsedFixtures from './parsedFixtures.json'
import DocumentPreview from '../../src/components/document-preview.vue'
import { docxFixture, docxImageFixture, sheetFixture, epubFixture } from './officeFixtures'

const otherBlob = ref<Blob | null>(null)
const otherExt = ref('')
const data = ref<ArrayBuffer | null>(null)
const locate = ref<SourceLocateRequest | null>(null)
const instance = ref(0)
const results = ref<string[]>([])
const finished = ref(false)
let done: ((r: PdfLocateResult) => void) | undefined
function assert(value: unknown, message: string) { if (!value) throw new Error(message) }
async function run(name: string, pages: Parameters<typeof fixturePdf>[0], request: Partial<SourceLocateRequest>, verify: (r: PdfLocateResult) => void | Promise<void>, documentData?: ArrayBuffer) {
  try {
    const result = new Promise<PdfLocateResult>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('render timed out')), 20000)
      done = (r) => { clearTimeout(timer); resolve(r) }
    })
    data.value = documentData || fixturePdf(pages)
    locate.value = { token: instance.value + 1, quotes: [], locators: [], ...request }
    instance.value++
    await nextTick()
    const r = await result
    await nextTick()
    for (const mark of document.querySelectorAll<HTMLElement>('.pdf-source-mark')) {
      for (const attr of ['left', 'top', 'width', 'height'] as const) assert(Number.isFinite(parseFloat(mark.style[attr])), `invalid ${attr}: ${mark.style.cssText}`)
      assert(parseFloat(mark.style.width) > 0 && parseFloat(mark.style.height) > 0, 'empty highlight')
    }
    await verify(r)
    results.value.push(`PASS ${name}`)
  } catch (e) { results.value.push(`FAIL ${name}: ${String(e)}`) }
}
async function runOther(name: string, blob: Blob, ext: string, request: Partial<SourceLocateRequest>, verify: (r: PdfLocateResult) => void) {
  try {
    const result = new Promise<PdfLocateResult>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('preview timed out')), 20000)
      done = (r) => { clearTimeout(timer); resolve(r) }
    })
    data.value = null
    otherBlob.value = blob
    otherExt.value = ext
    locate.value = { token: ++instance.value, quotes: [], locators: [], ...request }
    await nextTick()
    const r = await result
    await nextTick()
    verify(r)
    results.value.push(`PASS ${name}`)
  } catch (e) { results.value.push(`FAIL ${name}: ${String(e)}`) }
}
const marks = () => [...document.querySelectorAll<HTMLElement>('.pdf-source-mark--text')]
onMounted(async () => {
  if (new URLSearchParams(location.search).has('local')) {
    const cases = await fetch('./local/manifest.json').then(r => r.json())
    for (const fixture of cases) {
      const blob = await fetch('./local/' + fixture.file).then(r => r.blob())
      fixture.request.scope = sourceQuoteText(fixture.request.scope)
      if (fixture.ext === 'pdf') {
        otherBlob.value = null
        await run(fixture.name, [], fixture.request, r => {
          assert(r.found && r.precise === fixture.precise, JSON.stringify(r))
          if (fixture.firstPage) assert(r.page === fixture.firstPage, 'navigation must prioritize the cited instruction')
          const pages = [...new Set(marks().map(m => Number(m.closest('[data-page]')?.getAttribute('data-page'))))].sort((a, b) => a - b)
          assert(JSON.stringify(pages) === JSON.stringify(fixture.pages), `wrong highlighted pages: ${pages}`)
        }, await blob.arrayBuffer())
      } else await runOther(fixture.name, blob, fixture.ext, fixture.request, r => {
        assert(r.found && r.precise === fixture.precise, JSON.stringify(r))
        if (fixture.imageDigest) {
          const images = [...document.querySelectorAll<HTMLElement>('img.source-locate-block')]
          assert(images.length === 1 && images[0]!.dataset.sourceImageDigest === fixture.imageDigest, 'wrong source image')
        }
        if (fixture.highlightText) {
          const highlight = (CSS as any).highlights?.get('source-locate')
          const text = highlight ? [...highlight].map((range: Range) => range.toString()).join('') : [...document.querySelectorAll('.source-locate-mark')].map(e => e.textContent).join('')
          assert(text === fixture.highlightText, 'highlight must contain only the exact cited quotation')
        }
      })
    }
    finished.value = true
    return
  }
  for (const fixture of [
    { name: 'citation anchor retains all sentences preceding the marker', html: '<p>第一句说明原目录保留文件。第二句说明回到主仓库开发。<span class="citation">[1]</span></p>', want: '第一句说明原目录保留文件。第二句说明回到主仓库开发。' },
    { name: 'citation anchor stops at the previous citation', html: '<p>先前的内容属于另一个来源。<span class="citation">[1]</span>本条引用包括操作原理。以及后续开发所在的目录。<span class="citation">[2]</span></p>', want: '本条引用包括操作原理。以及后续开发所在的目录。' },
    { name: 'adjacent citations share the same preceding passage', html: '<p>两个来源共同支撑这段完整的解释。<span class="citation">[1]</span> <span class="citation">[2]</span></p>', want: '两个来源共同支撑这段完整的解释。' },
    { name: 'citation anchor retains inline code without citation labels', html: '<p><code>git switch --detach</code> 让原目录停在当前提交。后续开发在主仓库进行即可。<span class="citation-kb">filename.md</span></p>', want: 'git switch --detach 让原目录停在当前提交。后续开发在主仓库进行即可。' },
  ]) {
    try {
      const root = document.createElement('div')
      root.className = 'markdown-content'
      root.innerHTML = fixture.html
      const markers = root.querySelectorAll('.citation, .citation-kb')
      assert(citationAnchorText(markers[markers.length - 1]!) === fixture.want, 'wrong answer passage')
      results.value.push(`PASS ${fixture.name}`)
    } catch (e) { results.value.push(`FAIL ${fixture.name}: ${String(e)}`) }
  }
  const imageBytes = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='), c => c.charCodeAt(0))
  const imageDigest = await sourceImageDigest(imageBytes.buffer)
  await runOther('DOCX identifies image bytes without any text anchor', await docxImageFixture(), 'docx', { imageDigest }, r => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(document.querySelectorAll('img.source-locate-block').length === 1, 'exactly one image must be marked')
  })
  await runOther('DOCX rejects duplicate embedded image identities', await docxImageFixture(2), 'docx', { imageDigest }, r => assert(!r.found && r.reason === 'ambiguous', JSON.stringify(r)))
  await runOther('DOCX rejects a different image identity', await docxImageFixture(), 'docx', { imageDigest: '0'.repeat(64) }, r => assert(!r.found, JSON.stringify(r)))
  const fenced = '# Source document\n\nOpening unrelated paragraph.\n\n- **Explanation**: Releasing the branch preserves the current commit and files.\n\n```bash\ngit -C /example/tree switch --detach\n```'
  const highlightedText = () => {
    const highlight = (CSS as any).highlights?.get('source-locate')
    return highlight ? [...highlight].map((r: Range) => r.toString()).join('') : [...document.querySelectorAll('.source-locate-mark')].map(e => e.textContent).join('')
  }
  await runOther('Markdown fenced language is not treated as visible evidence', new Blob([fenced]), 'md', { sourceMarkdown: fenced }, r => assert(r.found && r.precise, JSON.stringify(r)))
  await runOther('Markdown paraphrase narrows within its verified source block', new Blob([fenced]), 'md', { sourceMarkdown: fenced, sentence: 'This operation preserves the current commit and files, freeing the branch.' }, r => {
    assert(r.found && !r.precise, JSON.stringify(r))
    assert(highlightedText() === 'Explanation: Releasing the branch preserves the current commit and files', highlightedText())
  })
  await runOther('Markdown exact code citation narrows to the command', new Blob([fenced]), 'md', { sourceMarkdown: fenced, sentence: 'git -C /example/tree switch --detach' }, r => {
    assert(r.precise, JSON.stringify(r)); assert(highlightedText() === 'git -C /example/tree switch --detach', highlightedText())
  })
  await runOther('Markdown duplicate complete sources stay ambiguous', new Blob([fenced + '\n\n' + fenced]), 'md', { sourceMarkdown: fenced }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('Markdown missing scope cannot match a similar paragraph', new Blob([fenced]), 'md', { sourceMarkdown: fenced + '\nMissing required evidence.', sentence: 'preserves the current commit and files' }, r => assert(!r.found, JSON.stringify(r)))
  const htmlCode = 'Literal code: `<Widget>` and `x < 1.5`.\n\n~~~html\n<section>example</section>\n~~~'
  await runOther('Markdown literal tags and tilde code fences survive projection', new Blob([htmlCode]), 'md', { sourceMarkdown: htmlCode }, r => assert(r.precise, JSON.stringify(r)))
  await runOther('Markdown changed numeric evidence is rejected', new Blob([htmlCode]), 'md', { sourceMarkdown: htmlCode.replace('1.5', '15') }, r => assert(!r.found, JSON.stringify(r)))
  const multiParagraph = '- **操作**：在原目录执行 `git switch --detach` 释放分支。\n- **原理**：`--detach` 让工作目录保留当前提交，现有文件仍然保留。\n- **后续**：完成操作以后可以回到主仓库继续开发。'
  await runOther('Markdown multi-sentence paraphrase locates both supported paragraphs', new Blob([multiParagraph]), 'md', {
    sourceMarkdown: multiParagraph, sentence: 'git switch --detach 让工作目录保留当前提交（detached HEAD），文件内容仍然保留。完成操作以后可以回到主仓库继续开发即可。',
  }, r => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(highlightedText() === '原理：--detach 让工作目录保留当前提交，现有文件仍然保留后续：完成操作以后可以回到主仓库继续开发', highlightedText())
  })
  const boundaryParagraph = 'Client only uses `api.example.test` for read-only requests. Tokens stay in the local keychain and never sync to the cloud.'
  const boundaryPrefix = '# Account settings\n\nChoose the personal repository.\n\nClient only uses `api.example.test`'
  const boundaryDocument = '# Account settings\n\nChoose the personal repository.\n\n' + boundaryParagraph + '\n\nUnrelated backup and restore instructions.'
  const completeBoundaryText = 'Client only uses api.example.test for read-only requests. Tokens stay in the local keychain and never sync to the cloud'
  await runOther('Markdown child ending mid-paragraph reveals the original complete paragraph', new Blob([boundaryDocument]), 'md', {
    sourceMarkdown: boundaryPrefix, sentence: 'Use api.example.test for read-only requests and keep tokens in the local keychain.',
  }, r => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(highlightedText() === completeBoundaryText, highlightedText())
  })
  await runOther('Markdown child starting mid-paragraph restores its opening without the next paragraph', new Blob([boundaryDocument]), 'md', {
    sourceMarkdown: 'Tokens stay in the local keychain and never sync to the cloud.', sentence: 'Credentials stay in the local keychain with no cloud synchronization.',
  }, r => {
    assert(r.found && !r.precise, JSON.stringify(r))
    assert(highlightedText() === completeBoundaryText, highlightedText())
  })
  await runOther('Markdown a complete explicit quotation remains narrowly highlighted', new Blob([boundaryDocument]), 'md', {
    sourceMarkdown: boundaryParagraph, sentence: 'The source says “Tokens stay in the local keychain and never sync to the cloud”.',
  }, r => {
    assert(r.precise, JSON.stringify(r))
    assert(highlightedText() === 'Tokens stay in the local keychain and never sync to the cloud', highlightedText())
  })
  await runOther('Markdown boundary completion cannot bypass duplicate source ambiguity', new Blob([boundaryDocument + '\n\n' + boundaryDocument]), 'md', {
    sourceMarkdown: boundaryPrefix, sentence: 'Use api.example.test for read-only requests.',
  }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('Markdown boundary completion cannot rescue a changed source value', new Blob([boundaryDocument]), 'md', {
    sourceMarkdown: boundaryPrefix.replace('api.example.test', 'api.other.test'), sentence: 'Tokens stay in the local keychain.',
  }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('Markdown an adjacent paragraph cannot be pulled in by answer similarity', new Blob([boundaryDocument]), 'md', {
    sourceMarkdown: boundaryPrefix, sentence: 'Unrelated backup and restore instructions.',
  }, r => {
    assert(r.found, JSON.stringify(r))
    assert(!highlightedText().includes('backup'), highlightedText())
  })
  const repeatedBoundary = '# First occurrence\n\n' + boundaryParagraph + '\n\n# Second occurrence\n\n' + boundaryParagraph
  await runOther('Markdown verified scope preserves the occurrence of a repeated paragraph', new Blob([repeatedBoundary]), 'md', {
    sourceMarkdown: '# Second occurrence\n\nClient only uses `api.example.test`', sentence: 'Use api.example.test for read-only requests and keep tokens in the local keychain.',
  }, r => {
    assert(r.found && !r.precise, JSON.stringify(r))
    assert(highlightedText() === completeBoundaryText, highlightedText())
    const range = [...(CSS as any).highlights.get('source-locate')][0] as Range
    assert(range.startContainer.parentElement?.closest('p')?.previousElementSibling?.textContent === 'Second occurrence', 'wrong repeated paragraph occurrence')
  })
  const docx = await docxFixture()
  await runOther('DOCX verifies the indexed paragraph', docx, 'docx', { locators: [{ type: 'docx', mapping: 'exact', block: 2, quote: 'The cited limit is 1.5 millimeters.' }] }, r => assert(r.precise, JSON.stringify(r)))
  await runOther('DOCX revalidates a legacy wrong block', docx, 'docx', { scope: 'The cited limit is 1.5 millimeters.', locators: [{ type: 'docx', block: 1 }] }, r => assert(r.precise, JSON.stringify(r)))
  await runOther('DOCX rejects a different numeric value', docx, 'docx', { scope: 'The cited limit is 15 millimeters.', locators: [{ type: 'docx', block: 2 }] }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('DOCX partial locator still verifies the exact cited sentence', docx, 'docx', {
    scope: 'The cited limit is 1.5 millimeters. Unmapped image placeholder.', sentence: 'The cited limit is 1.5 millimeters.',
    locators: [{ type: 'docx', mapping: 'exact', partial: true, block: 2, quote: 'The cited limit is 1.5 millimeters.' }],
  }, r => assert(r.precise, JSON.stringify(r)))
  const imageContext = { before: 'The cited limit is 1.5 millimeters.', after: 'Verified paragraph after the image.' }
  await runOther('DOCX OCR citation highlights its unique bracketed image', await docxImageFixture(), 'docx', { imageContext }, r => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(document.querySelectorAll('img.source-locate-block').length === 1, 'wrong image region')
  })
  await runOther('DOCX multiple bracketed images cannot silently choose the first', await docxImageFixture(2), 'docx', { imageContext }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('DOCX missing image context cannot highlight another image', await docxImageFixture(), 'docx', { imageContext: { ...imageContext, after: 'Missing paragraph.' } }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('DOCX image at chunk end stops before the next original paragraph', await docxImageFixture(), 'docx', { imageContext: { ...imageContext, after: '' } }, r => assert(r.found && !r.precise, JSON.stringify(r)))
  await runOther('DOCX image at chunk start uses its following original paragraph', await docxImageFixture(), 'docx', { imageContext: { ...imageContext, before: '' } }, r => assert(r.found && !r.precise, JSON.stringify(r)))
  const sheet = sheetFixture()
  await runOther('spreadsheet shows verified rows as a region', sheet, 'xlsx', { locators: [{ type: 'sheet', mapping: 'exact', sheet: 'Inspection', row_start: 2, row_end: 2, quote: 'Anchor 1.5 mm' }] }, r => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(document.querySelector('tr.source-locate-block')?.textContent?.includes('Anchor'), 'wrong row')
  })
  await runOther('spreadsheet missing sheet cannot select first sheet', sheet, 'xlsx', { locators: [{ type: 'sheet', mapping: 'exact', sheet: 'Missing', row_start: 2 }] }, r => assert(!r.found, JSON.stringify(r)))
  const epub = await epubFixture()
  await runOther('EPUB exact section disambiguates repeated text', epub, 'epub', { locators: [{ type: 'section', mapping: 'exact', section: 2, quote: 'Repeated cited passage.' }] }, r => assert(r.precise, JSON.stringify(r)))
  await runOther('EPUB legacy duplicates cannot choose first chapter', epub, 'epub', { scope: 'Repeated cited passage.', locators: [{ type: 'section', section: 1 }] }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('Markdown repeated evidence is not silently selected', new Blob(['First chapter.\n\nRepeated cited passage.\n\nSecond chapter.\n\nRepeated cited passage.']), 'md', { scope: 'Repeated cited passage.' }, r => assert(!r.found, JSON.stringify(r)))
  await runOther('plain text revalidates stale character offsets', new Blob(['Wrong opening.\nUnique cited passage.']), 'txt', { scope: 'Unique cited passage.', locators: [{ type: 'text', start: 0, end: 10, quote: 'Unique cited passage.' }] }, r => assert(r.precise, JSON.stringify(r)))
  await runOther('edited evidence cannot reuse an original text location', new Blob(['Unique cited passage.']), 'txt', { unavailable: true, scope: 'Unique cited passage.' }, r => assert(!r.found && r.reason === 'unavailable', JSON.stringify(r)))
  otherBlob.value = null

  await run('legacy wrong page resolves to page 2', [
    { lines: [['Unrelated first page.', 60, 620]] }, { lines: [['Unique cited source passage.', 60, 620]] },
  ], { scope: 'Unique cited source passage.', locators: [{ type: 'pdf', page: 1, bbox: [0, 0, 1, 1], quote: 'Unique cited source passage.' }] }, (r) => {
    assert(r.page === 2 && r.precise, JSON.stringify(r))
    assert(marks().every((m) => m.closest('[data-page]')?.getAttribute('data-page') === '2'), 'wrong page highlight')
  })
  await run('table columns verify repeated course names against the paired teacher rows', [
    { lines: [['Drawing Bob RoomA', 60, 650], ['Drawing Alice RoomB', 60, 620], ['Robotics Carol RoomC', 60, 590], ['Dance David RoomD', 60, 560], ['Music Ellen RoomE', 60, 530], ['Chess Frank RoomF', 60, 500]] },
    { lines: [['Drawing Alice RoomA', 60, 650], ['Drawing Bob RoomB', 60, 620], ['Robotics Carol RoomC', 60, 590], ['Dance David RoomD', 60, 560], ['Music Ellen RoomE', 60, 530], ['Chess Frank RoomF', 60, 500]] },
  ], { scope: '|Drawing Drawing Robotics Dance Music Chess|Alice Bob Carol David Ellen Frank|' }, r => {
    assert(r.found && r.page === 2 && r.reason === 'partial', JSON.stringify(r))
    assert(marks().length >= 12 && marks().every(m => m.closest('[data-page]')?.getAttribute('data-page') === '2'), 'table paired to the wrong rows')
  })
  await run('image-heavy cross-page instructions retain verified excerpts', [
    { lines: [['Step two: choose the desktop view.', 60, 650]] },
    { lines: [['Step three: open the application.', 60, 650], ['Select the application form.', 60, 600]] },
  ], { scope: 'Step two: choose the desktop view.\nStep three: open the application.\nSelect the application form.\n' + 'Unmatched image OCR details. '.repeat(30), sentence: 'Select the application form.' }, r => {
    assert(r.found && r.page === 2 && r.reason === 'partial', JSON.stringify(r))
    assert(document.querySelector('[data-page="2"] .pdf-source-mark--text'), 'missing actual instruction')
  })
  await run('duplicate text reports ambiguity', [{ lines: [['Repeated source passage.', 60, 620], ['Repeated source passage.', 60, 420]] }], { scope: 'Repeated source passage.' }, (r) => {
    assert(r.reason === 'ambiguous' && !r.found && marks().length === 0, JSON.stringify(r))
  })
  await run('decimal mismatch has no highlight', [{ lines: [['The limit is 15 millimeters.', 60, 620]] }], { scope: 'The limit is 1.5 millimeters.' }, (r) => assert(!r.found && !marks().length, JSON.stringify(r)))
  await run('bbox disambiguates the lower repeated paragraph', [{ lines: [['Repeated source passage.', 60, 620], ['Repeated source passage.', 60, 420]] }], {
    locators: [{ type: 'pdf', mapping: 'exact', page: 1, quote: 'Repeated source passage.', bbox: [0.09, 0.44, 0.8, 0.5] }],
  }, (r) => { assert(r.precise, JSON.stringify(r)); assert(marks().every((m) => parseFloat(m.style.top) > 40), 'upper duplicate highlighted') })
  await run('mixed scanned region and text page retain both', [{ lines: [] }, { lines: [['Second page source passage.', 60, 620]] }], {
    locators: [{ type: 'pdf', mapping: 'exact', page: 1, bbox: [0.1, 0.2, 0.8, 0.4], quote: 'Scanned text' }, { type: 'pdf', mapping: 'exact', page: 2, quote: 'Second page source passage.' }],
  }, (r) => {
    assert(r.found && !r.precise && r.granularity === 'block', JSON.stringify(r))
    assert(document.querySelector('[data-page="1"] .pdf-source-mark--box'), 'missing scan region')
    assert(document.querySelector('[data-page="2"] .pdf-source-mark--text'), 'missing second page')
  })
  await run('rotated page text uses rendered geometry', [{ rotation: 90, lines: [['Rotated cited passage.', 60, 620]] }], { scope: 'Rotated cited passage.' }, (r) => {
    assert(r.precise && marks().length > 0, JSON.stringify(r))
    assert(marks().every((m) => parseFloat(m.style.top) >= 0 && parseFloat(m.style.left) >= 0), 'invalid rotated geometry')
  })
  await run('cross-page evidence highlights both pages', [{ lines: [['First half of the', 60, 620]] }, { lines: [['source continues here.', 60, 620]] }], { scope: 'First half of the source continues here.' }, (r) => {
    assert(r.precise, JSON.stringify(r))
    assert(document.querySelector('[data-page="1"] .pdf-source-mark--text') && document.querySelector('[data-page="2"] .pdf-source-mark--text'), 'missing cross-page evidence')
  })
  for (const fixture of parsedFixtures) {
    const buffer = Uint8Array.from(atob(fixture.pdf), (c) => c.charCodeAt(0)).buffer
    await run(fixture.name, [], { locators: [fixture.locator] }, (r) => {
      assert(r.precise && r.page === fixture.page, JSON.stringify(r))
      assert(marks().length > 0, 'missing parser-derived highlight')
      for (const mark of marks()) {
        const left = parseFloat(mark.style.left) / 100, top = parseFloat(mark.style.top) / 100
        const width = parseFloat(mark.style.width) / 100, height = parseFloat(mark.style.height) / 100
        const [x0, y0, x1, y1] = fixture.locator.bbox
        assert(left + width / 2 >= x0 && left + width / 2 <= x1 && top + height / 2 >= y0 && top + height / 2 <= y1, 'highlight outside parser source region')
      }
    }, buffer)
  }
  finished.value = true
})
</script>

<template>
  <main>
    <h1>Source location: document rendering regression</h1>
    <p id="summary">{{ finished ? `${results.filter((r) => r.startsWith('PASS')).length}/${results.length} passed` : 'Running…' }}</p>
    <details :open="!finished"><summary>Case results</summary><ul><li v-for="r in results" :key="r">{{ r }}</li></ul></details>
    <div class="viewer"><DocumentPreview v-if="otherBlob" :key="instance" :source-blob="otherBlob" :file-type="otherExt" :file-name="`fixture.${otherExt}`" :active="true" source-mode fill-height :locate="locate" @located="(r) => done?.(r)" /><PdfSourceViewer v-if="data" :key="instance" :data="data" :locate="locate" @located="(r) => done?.(r)" /></div>
  </main>
</template>

<style>
body { margin: 24px; font: 14px system-ui; --app-source-highlight: #ffc400; }
.viewer { width: min(760px, 100%); height: 600px; border: 1px solid #ddd; }
li { margin: 5px 0; }
</style>
