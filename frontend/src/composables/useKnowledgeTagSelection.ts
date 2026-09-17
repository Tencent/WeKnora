import { computed, ref, watch, type Ref } from 'vue'

export interface KnowledgeTag {
  id: string
  name: string
  color?: string
  knowledge_count?: number
}

interface TagSelectionOptions {
  visible: () => boolean
  kbId: () => string
  tags: () => KnowledgeTag[]
  selectedIds: () => string[]
  createTag: (kbId: string, data: { name: string }) => Promise<KnowledgeTag | { data: KnowledgeTag }>
  onCreated: () => void
  onError: (error: unknown) => void
}

/** Shared selection and creation state; each dialog owns its submit/close behavior. */
export function useKnowledgeTagSelection(options: TagSelectionOptions) {
  const searchQuery = ref('')
  const newTagName = ref('')
  const selectedSet = ref(new Set<string>())
  const creatingTag = ref(false)

  watch(options.visible, (visible) => {
    if (visible) {
      selectedSet.value = new Set(options.selectedIds())
      searchQuery.value = ''
      newTagName.value = ''
    }
  })

  const tagMap = computed(() => new Map(options.tags().map(tag => [tag.id, tag])))
  const selectedTagsList = computed(() => Array.from(selectedSet.value)
    .map(id => tagMap.value.get(id))
    .filter((tag): tag is KnowledgeTag => Boolean(tag)))
  const availableTagsList = computed(() => {
    const query = searchQuery.value.trim().toLowerCase()
    return options.tags().filter(tag => !selectedSet.value.has(tag.id)
      && (!query || (tag.name || '').toLowerCase().includes(query)))
  })

  function selectTag(id: string) {
    selectedSet.value = new Set([...selectedSet.value, id])
  }

  function toggleTag(id: string) {
    const next = new Set(selectedSet.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    selectedSet.value = next
  }

  function clearAll() {
    selectedSet.value = new Set()
  }

  async function createFrom(input: Ref<string>, reuseExisting: boolean) {
    const name = input.value.trim()
    if (!name) return
    const existing = reuseExisting && options.tags().find(tag => tag.name === name)
    if (existing) {
      selectTag(existing.id)
      input.value = ''
      return
    }
    creatingTag.value = true
    try {
      const response = await options.createTag(options.kbId(), { name })
      const tag = 'data' in response ? response.data : response
      selectTag(tag.id)
      input.value = ''
      options.onCreated()
    } catch (error) {
      options.onError(error)
    } finally {
      creatingTag.value = false
    }
  }

  return {
    searchQuery, newTagName, selectedSet, creatingTag, selectedTagsList, availableTagsList,
    toggleTag, clearAll,
    handleCreateTag: () => createFrom(searchQuery, false),
    handleAddNewTag: () => createFrom(newTagName, true),
  }
}
