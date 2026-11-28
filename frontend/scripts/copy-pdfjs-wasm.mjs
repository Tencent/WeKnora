import { cpSync, existsSync, mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const assetDirectories = [
  ['cmaps', 'Adobe-GB1-UCS2.bcmap'],
  ['standard_fonts', 'LiberationSans-Regular.ttf'],
  ['wasm', 'jbig2.wasm'],
]

/** Copy the installed pdfjs-dist runtime resources into the frontend build. */
export function copyPdfJsAssets(packageDir, outputDir) {
  for (const [directory, requiredAsset] of assetDirectories) {
    const sourceDir = resolve(packageDir, directory)
    const requiredPath = resolve(sourceDir, requiredAsset)
    if (!existsSync(requiredPath)) {
      throw new Error(`PDF.js ${directory} resources are missing the required asset: ${requiredPath}`)
    }

    const assetOutput = resolve(outputDir, directory)
    mkdirSync(assetOutput, { recursive: true })
    cpSync(sourceDir, assetOutput, { recursive: true })
  }
}
