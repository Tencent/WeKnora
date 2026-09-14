/** Tool that writes skill output into the session artifact directory. */
export const EXECUTE_SKILL_SCRIPT_TOOL = 'execute_skill_script'

export type SkillArtifactStreamEvent = {
  tool_name?: string
}

export type SkillArtifactSessionLike = {
  artifacts?: unknown[]
  is_completed?: boolean
  artifactsCollecting?: boolean
  agentEventStream?: SkillArtifactStreamEvent[]
}

export function agentTurnUsedSkillScript(
  stream: SkillArtifactStreamEvent[] | null | undefined,
): boolean {
  if (!Array.isArray(stream)) return false
  return stream.some((event) => event?.tool_name === EXECUTE_SKILL_SCRIPT_TOOL)
}

/**
 * True while the assistant answer is already on screen but generated files
 * have not been persisted yet. The server uploads sandbox output after the
 * answer `done` event, so the toolbar would otherwise sit empty for seconds.
 */
export function isCollectingSkillArtifacts(
  session: SkillArtifactSessionLike | null | undefined,
): boolean {
  if (!session) return false
  if (Array.isArray(session.artifacts) && session.artifacts.length > 0) return false
  if (session.is_completed) return false
  if (session.artifactsCollecting) return true
  return agentTurnUsedSkillScript(session.agentEventStream)
}
