import { regeneratePrunedLocaleFiles } from './localeKeyAudit.ts'

const { before, after } = await regeneratePrunedLocaleFiles()
console.log(`Pruned locale keys: ${before} -> ${after} (en-US reference)`)
