import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const versionFile = resolve(import.meta.dirname, '../../VERSION')

/** 仓库根目录 VERSION 文件中的发布版本（与 scripts/get_version.sh 同源） */
export function getRepoVersion(): string {
  if (!existsSync(versionFile)) return 'unknown'
  return readFileSync(versionFile, 'utf-8').trim() || 'unknown'
}

export const repoVersion = getRepoVersion()

export const repoVersionLabel =
  repoVersion === 'unknown' || repoVersion.startsWith('v') ? repoVersion : `v${repoVersion}`
