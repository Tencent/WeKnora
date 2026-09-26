// The three pages of the plugin. They run in a sandboxed iframe: no network,
// no storage of the app. Everything goes through the bridge (wk.*), which
// the app relays to main.py.
import { connect } from './weknora-plugin-ui.js'

const TEXT = {
  en: {
    empty: 'No links yet.', emptyManage: 'No links yet. Add the first one below.',
    title: 'Title', url: 'https://…', add: 'Add', remove: 'Remove', saved: 'Links saved',
    confirm: 'Remove this link?', readOnly: 'Only editors of the workspace can change these links.',
  },
  zh: {
    empty: '还没有链接。', emptyManage: '还没有链接，在下方添加第一个。',
    title: '标题', url: 'https://…', add: '添加', remove: '删除', saved: '链接已保存',
    confirm: '删除这个链接？', readOnly: '只有空间的编辑者可以修改这些链接。',
  },
}

const wk = await connect()
const app = document.getElementById('app')
const mode = document.body.dataset.mode
let text = pick(wk.context.locale)
wk.on('locale', (locale) => {
  text = pick(locale)
  void load()
})
// A knowledge base tab follows the app to another knowledge base.
wk.on('init', () => void load())

function pick(locale) {
  return String(locale || '').startsWith('zh') ? TEXT.zh : TEXT.en
}

function path() {
  return mode === 'kb' ? `/kb/${wk.context.context.knowledgeBaseId}/links` : '/links'
}

function el(tag, props = {}, ...children) {
  const node = Object.assign(document.createElement(tag), props)
  node.append(...children)
  return node
}

async function load() {
  app.setAttribute('aria-busy', 'true')
  try {
    const { body } = await wk.get(path())
    render(Array.isArray(body) ? body : [])
  } catch (e) {
    app.replaceChildren(el('p', { className: 'wk-empty', textContent: e.message }))
  } finally {
    app.removeAttribute('aria-busy')
  }
}

async function save(links) {
  try {
    const { body } = await wk.put(path(), links)
    await wk.toast(text.saved, 'success')
    render(body)
  } catch (e) {
    await wk.toast(e.message, 'error')
  }
}

function render(links) {
  const editable = mode === 'manage' || (mode === 'kb' && wk.context.role !== 'viewer')
  const list = el('ul', { className: 'wk-list' })
  links.forEach((link, i) => {
    const title = el('div', { className: 'wk-row__title' },
      el('a', { href: link.url, target: '_blank', rel: 'noopener noreferrer', textContent: link.title || link.url }))
    const item = el('li', { className: 'wk-row' },
      el('span', { className: 'wk-avatar', textContent: initial(link) }),
      el('div', { className: 'wk-row__main' }, title, el('div', { className: 'wk-row__sub', textContent: link.url })))
    if (editable) {
      const remove = el('button', { className: 'wk-btn wk-btn--text wk-btn--danger wk-btn--sm', type: 'button', textContent: text.remove })
      remove.addEventListener('click', async () => {
        if (await wk.confirm(text.confirm)) await save(links.filter((_, j) => j !== i))
      })
      item.append(el('div', { className: 'wk-row__actions' }, remove))
    }
    list.append(item)
  })
  const nodes = links.length ? [list] : [el('p', { className: 'wk-empty', textContent: mode === 'manage' ? text.emptyManage : text.empty })]
  if (editable) {
    const title = el('input', { className: 'wk-input', placeholder: text.title, maxLength: 120 })
    const url = el('input', { className: 'wk-input', placeholder: text.url, type: 'url', required: true })
    const form = el('form', { className: 'wk-toolbar wk-toolbar--below' }, title, url,
      el('button', { className: 'wk-btn wk-btn--primary', type: 'submit', textContent: text.add }))
    form.addEventListener('submit', async (event) => {
      event.preventDefault()
      await save([...links, { title: title.value, url: url.value }])
    })
    nodes.push(form)
  }
  app.replaceChildren(...nodes)
}

// The first letter of a link's title, or of its host.
function initial(link) {
  let s = link.title
  if (!s) {
    try {
      s = new URL(link.url).hostname.replace(/^www\./, '')
    } catch {
      s = link.url
    }
  }
  return (s || '?').trim().charAt(0).toUpperCase()
}

await load()
