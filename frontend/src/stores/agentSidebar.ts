import { ref } from 'vue';

// Global open/closed state for the agent sidebar dock (agent-sidebar-inbox).
// The dock is app-wide: App.vue mounts it outside any page component, so the
// visibility flag must live outside the component tree too. Pages that need
// to react to the dock (e.g. KnowledgeBase's folder-tree auto-collapse)
// import this ref instead of wiring v-model through props.
export const agentSidebarVisible = ref(false);
