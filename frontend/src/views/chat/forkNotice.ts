/**
 * Fork notice copy.
 *
 * A degraded fork still succeeded — the branch exists and the conversation is
 * intact — so the wording explains what was NOT carried over rather than
 * reading as an error. A non-degraded fork still needs a note: the sandbox
 * working tree is rolled back to the fork point, but installed packages are
 * from the fork *moment*.
 */

const FORK_DEGRADE_COPY: Record<string, string> = {
  NO_CHECKPOINT: '未能复制沙箱环境（该轮未创建检查点），已为分支创建全新环境',
  SANDBOX_REPLACED: '原会话中途更换过沙箱，未能复制环境，已创建全新环境',
  SANDBOX_GONE: '原会话沙箱已回收，未能复制环境，已创建全新环境',
  SNAPSHOT_UNSUPPORTED: '当前沙箱后端不支持环境复制，已创建全新环境',
}

const FORK_SUCCESS_COPY = '工作区已回到分叉点；已安装的依赖仍是分叉操作当时的环境'

/** Returns the banner text for a degrade reason, or '' when none applies. */
export function forkDegradeMessage(reason: string): string {
  if (!reason) return ''
  return FORK_DEGRADE_COPY[reason] ?? '未能复制沙箱环境，已创建全新环境'
}

/** One-shot note for a non-degraded successful fork. */
export function forkSuccessMessage(): string {
  return FORK_SUCCESS_COPY
}
