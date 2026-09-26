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
    app.replaceChildren(el('p', { className: 'empty', textContent: e.message }))
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
  const list = el('ul', { className: 'links' })
  links.forEach((link, i) => {
    const a = el('a', { href: link.url, target: '_blank', rel: 'noopener noreferrer', textContent: link.title })
    const item = el('li', {}, a, el('span', { className: 'url', textContent: link.url }))
    if (editable) {
      const remove = el('button', { className: 'remove', type: 'button', textContent: text.remove })
      remove.addEventListener('click', async () => {
        if (await wk.confirm(text.confirm)) await save(links.filter((_, j) => j !== i))
      })
      item.append(remove)
    }
    list.append(item)
  })
  const nodes = links.length ? [list] : [el('p', { className: 'empty', textContent: mode === 'manage' ? text.emptyManage : text.empty })]
  if (editable) {
    const title = el('input', { placeholder: text.title, maxLength: 120 })
    const url = el('input', { placeholder: text.url, type: 'url', required: true })
    const form = el('form', {}, title, url, el('button', { type: 'submit', textContent: text.add }))
    form.addEventListener('submit', async (event) => {
      event.preventDefault()
      await save([...links, { title: title.value, url: url.value }])
    })
    nodes.push(form)
  }
  app.replaceChildren(...nodes)
}

await load()
