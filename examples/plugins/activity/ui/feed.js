import { connect } from './weknora-plugin-ui.js'

const LABELS = {
  en: { ingested: 'Ingested', failed: 'Failed', deleted: 'Deleted', answered: 'Answered', note: 'Note', empty: 'Nothing yet.', refresh: 'Refresh' },
  zh: { ingested: '已入库', failed: '失败', deleted: '已删除', answered: '已回答', note: '消息', empty: '暂无动态。', refresh: '刷新' },
}

const wk = await connect()
const app = document.getElementById('app')
let text = pick(wk.context.locale)
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
    render(Array.isArray(body) ? body : [])
  } catch (e) {
    app.replaceChildren(el('p', { className: 'empty', textContent: e.message }))
  } finally {
    app.removeAttribute('aria-busy')
  }
}

function render(feed) {
  const refresh = el('button', { type: 'button', textContent: text.refresh })
  refresh.addEventListener('click', () => void load())
  const list = el('ol')
  for (const entry of feed) {
    const at = new Date(entry.at)
    list.append(
      el('li', {},
        el('span', { className: `kind ${entry.kind}`, textContent: text[entry.kind] || entry.kind }),
        el('span', { className: 'text', textContent: entry.text }),
        el('time', { dateTime: entry.at, textContent: isNaN(at) ? '' : at.toLocaleString(wk.context.locale) })),
    )
  }
  app.replaceChildren(el('div', { className: 'bar' }, refresh), feed.length ? list : el('p', { className: 'empty', textContent: text.empty }))
}

await load()
