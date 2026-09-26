import { connect } from './weknora-plugin-ui.js'

const LABELS = {
  en: { ingested: 'Ingested', failed: 'Failed', deleted: 'Deleted', answered: 'Answered', note: 'Note', empty: 'Nothing yet.', refresh: 'Refresh', all: 'All', qa: 'Q&A', docs: 'Documents' },
  zh: { ingested: '已入库', failed: '失败', deleted: '已删除', answered: '已回答', note: '消息', empty: '暂无动态。', refresh: '刷新', all: '全部', qa: '问答', docs: '入库' },
}

// The feed's filters: which entry kinds each shows.
const FILTERS = { all: null, qa: ['answered'], docs: ['ingested', 'deleted'], failed: ['failed'] }
const TAG = { ingested: 'wk-tag--success', answered: 'wk-tag--brand', failed: 'wk-tag--danger', deleted: '', note: '' }

const wk = await connect()
const app = document.getElementById('app')
let text = pick(wk.context.locale)
let filter = 'all'
let feed = []
wk.on('locale', (locale) => {
  text = pick(locale)
  void load()
})

function pick(locale) {
  return String(locale || '').startsWith('zh') ? LABELS.zh : LABELS.en
}

function el(tag, props = {}, ...children) {
  const node = Object.assign(document.createElement(tag), props)
  node.append(...children)
  return node
}

async function load() {
  app.setAttribute('aria-busy', 'true')
  try {
    const { body } = await wk.get('/feed')
    feed = Array.isArray(body) ? body : []
    render()
  } catch (e) {
    app.replaceChildren(el('p', { className: 'wk-empty', textContent: e.message }))
  } finally {
    app.removeAttribute('aria-busy')
  }
}

function render() {
  const seg = el('div', { className: 'wk-seg', role: 'group' })
  for (const key of Object.keys(FILTERS)) {
    const b = el('button', { type: 'button', textContent: text[key] })
    b.setAttribute('aria-pressed', String(filter === key))
    b.addEventListener('click', () => {
      filter = key
      render()
    })
    seg.append(b)
  }
  const refresh = el('button', { className: 'wk-btn wk-btn--sm', type: 'button', textContent: text.refresh })
  refresh.addEventListener('click', () => void load())

  const kinds = FILTERS[filter]
  const shown = kinds ? feed.filter((entry) => kinds.includes(entry.kind)) : feed
  const list = el('ol', { className: 'wk-list' })
  for (const entry of shown) {
    const at = new Date(entry.at)
    list.append(
      el('li', { className: 'wk-row' },
        el('span', { className: `wk-tag ${TAG[entry.kind] ?? ''}`, textContent: text[entry.kind] || entry.kind }),
        el('span', { className: 'wk-row__main', textContent: entry.text }),
        el('time', { className: 'wk-row__meta', dateTime: entry.at, textContent: isNaN(at) ? '' : at.toLocaleString(wk.context.locale) })),
    )
  }
  app.replaceChildren(
    el('div', { className: 'wk-toolbar wk-toolbar--end' }, seg, refresh),
    shown.length ? list : el('p', { className: 'wk-empty', textContent: text.empty }),
  )
}

await load()
