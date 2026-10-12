import { computed, ref } from 'vue';

// Global open/closed state for the agent sidebar dock (agent-sidebar-inbox).
// The dock is app-wide: App.vue mounts it outside any page component, so the
// visibility flag must live outside the component tree too. Pages that need
// to react to the dock (e.g. KnowledgeBase's folder-tree auto-collapse)
// import this ref instead of wiring v-model through props.
export const agentSidebarVisible = ref(false);

// Live dock width in px; 0 while the dock is closed. AgentSidebarDrawer
// keeps this in sync (same source as the --agent-sidebar-w CSS var it
// publishes). JS consumers that must do pixel math — e.g. SettingDrawer's
// width clamping and maximize sizing — read the computed inset below
// instead of parsing the CSS var back out of the DOM.
export const agentSidebarWidth = ref(0);

export const agentSidebarInset = computed(() =>
  agentSidebarVisible.value ? agentSidebarWidth.value : 0
);
