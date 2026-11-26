// Vietnamese locale. Keys not translated yet fall back to English,
// so new strings never render as raw keys.
import enUS from './en-US.ts'
import agent from './vi-VN/agent.ts'
import auth from './vi-VN/auth.ts'
import chat from './vi-VN/chat.ts'
import common from './vi-VN/common.ts'
import datasource from './vi-VN/datasource.ts'
import knowledge from './vi-VN/knowledge.ts'
import model from './vi-VN/model.ts'
import settings from './vi-VN/settings.ts'
import system from './vi-VN/system.ts'

type Tree = Record<string, unknown>
const isTree = (v: unknown): v is Tree => !!v && typeof v === 'object' && !Array.isArray(v)
function merge(base: Tree, patch: Tree): Tree {
  const out: Tree = { ...base }
  for (const [k, v] of Object.entries(patch)) out[k] = isTree(v) && isTree(out[k]) ? merge(out[k] as Tree, v) : v
  return out
}

export default merge(enUS as Tree, {
  ...agent,
  ...auth,
  ...chat,
  ...common,
  ...datasource,
  ...knowledge,
  ...model,
  ...settings,
  ...system,
}) as typeof enUS
