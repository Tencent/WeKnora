import { nextTick, onMounted, ref, watch, type Ref } from 'vue'

/**
 * One-shot attention motion when generated files first land on a live
 * answer. Historical messages already have artifacts at mount and stay still.
 */
export function useArtifactArriveMotion(artifactCount: Ref<number>) {
  const artifactArrived = ref(false)
  let armed = false

  onMounted(() => {
    requestAnimationFrame(() => {
      armed = true
    })
  })

  watch(artifactCount, (count, previous) => {
    if (!armed || count <= 0 || (previous ?? 0) > 0) return
    artifactArrived.value = false
    nextTick(() => {
      artifactArrived.value = true
    })
  })

  function onArtifactArriveEnd(event?: AnimationEvent) {
    if (event?.animationName && event.animationName !== 'artifact-toolbar-arrive') return
    artifactArrived.value = false
  }

  return { artifactArrived, onArtifactArriveEnd }
}
