/**
 * Helpers for rendering shell_exec tool cards.
 * Live results carry stdout/stderr on tool_data; older turns only have the
 * markdown Output blob that used to be shown as "raw output".
 */

export type ShellExecView = {
  command: string
  workDir: string
  exitCode: number | null
  durationMs: number | null
  killed: boolean
  truncated: boolean
  stdoutBinary: boolean
  stderrBinary: boolean
  stdout: string
  stderr: string
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>
      }
    } catch {
      return {}
    }
  }
  return {}
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : value == null ? '' : String(value)
}

function asNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const n = Number(value)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function asBool(value: unknown): boolean {
  return value === true || value === 'true'
}

function extractFencedSection(output: string, heading: string): string {
  const headingMatch = output.match(new RegExp(`##\\s+${heading}\\b[\\s\\S]*?\`\`\``, 'i'))
  if (!headingMatch) return ''
  const afterOpenFence = output.slice(output.indexOf(headingMatch[0]) + headingMatch[0].length)
  const start = afterOpenFence.startsWith('\n') ? 1 : 0
  const end = afterOpenFence.indexOf('```', start)
  const body = end >= 0 ? afterOpenFence.slice(start, end) : afterOpenFence.slice(start)
  return body.replace(/^\n/, '').replace(/\n$/, '')
}

function stripLegacyShellHeader(output: string): string {
  return output
    .replace(/^=== Shell Exec[\s\S]*?\n\n/, '')
    .replace(/^=== Script Execution[\s\S]*?\n\n/, '')
    .replace(/\*\*Command\*\*:[\s\S]*?(?:\n(?!\*\*)|$)/, '')
    .replace(/\*\*Arguments\*\*:.*\n/, '')
    .replace(/\*\*Work Dir\*\*:.*\n/, '')
    .replace(/\*\*Exit Code\*\*:.*\n/, '')
    .replace(/\*\*Duration\*\*:.*\n/, '')
    .replace(/\*\*Killed\*\*:.*\n/, '')
    .replace(/\*\*Truncated\*\*:.*\n/, '')
    .replace(/\*\*Binary Output Suppressed\*\*:.*\n/, '')
    .trim()
}

function skillScriptCommand(
  data: Record<string, unknown>,
  args: Record<string, unknown>,
): string {
  const skill = asString(data.skill_name) || asString(args.skill_name)
  const script = asString(data.script_path) || asString(args.script_path)
  if (!skill && !script) return ''
  const path = [skill, script].filter(Boolean).join('/')
  const extra = data.args ?? args.args
  if (Array.isArray(extra) && extra.length) {
    return [path, ...extra.map((item) => asString(item))].join(' ')
  }
  return path
}

export function buildShellExecView(
  data: Record<string, unknown> | null | undefined,
  args: unknown,
  output?: string,
): ShellExecView {
  const record = data || {}
  const argumentsRecord = asRecord(args)
  const rawOutput = asString(output)
  const stdoutFromData = asString(record.stdout)
  const stderrFromData = asString(record.stderr)
  const parsedStdout = stdoutFromData
    ? ''
    : extractFencedSection(rawOutput, 'Stdout') || extractFencedSection(rawOutput, 'Standard Output')
  const parsedStderr = stderrFromData
    ? ''
    : extractFencedSection(rawOutput, 'Stderr') || extractFencedSection(rawOutput, 'Standard Error')

  let stdout = stdoutFromData || parsedStdout
  let stderr = stderrFromData || parsedStderr
  if (!stdout && !stderr && rawOutput) {
    stdout = stripLegacyShellHeader(rawOutput)
  }

  return {
    command: asString(record.command) || asString(argumentsRecord.command) || skillScriptCommand(record, argumentsRecord),
    workDir: asString(record.work_dir) || asString(argumentsRecord.work_dir),
    exitCode: asNumber(record.exit_code),
    durationMs: asNumber(record.duration_ms),
    killed: asBool(record.killed),
    truncated: asBool(record.truncated) || asBool(record.stdout_truncated) || asBool(record.stderr_truncated),
    stdoutBinary: asBool(record.stdout_binary),
    stderrBinary: asBool(record.stderr_binary),
    stdout,
    stderr,
  }
}

export function previewShellCommand(command: string, max = 72): string {
  const trimmed = command.replace(/\s+/g, ' ').trim()
  if (trimmed.length <= max) return trimmed
  return `${trimmed.slice(0, max - 1)}…`
}
