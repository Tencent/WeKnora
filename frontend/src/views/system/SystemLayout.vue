<template>
  <!--
    SystemLayout — top-level shell for the platform-wide administration
    area (/platform/system/*). Gated by meta.requiresSystemAdmin in the
    router; reaching this component means the caller is an authenticated
    SystemAdmin.

    Sidebar is intentionally simple: global system settings live inside
    the standard Settings modal, while this routed area keeps workflows
    that need a table-oriented management surface.
  -->
  <div class="system-layout">
    <aside class="system-sidebar">
      <div class="system-sidebar-header">
        <h2 class="system-sidebar-title">系统管理</h2>
        <div class="system-sidebar-subtitle">SystemAdmin</div>
      </div>
      <nav class="system-nav">
        <router-link
          v-for="item in navItems"
          :key="item.name"
          :to="{ name: item.name }"
          v-slot="{ isActive, navigate }"
          custom
        >
          <div
            :class="['system-nav-item', { active: isActive }]"
            @click="navigate"
          >
            <t-icon :name="item.icon" class="system-nav-icon" />
            <span class="system-nav-label">{{ item.label }}</span>
          </div>
        </router-link>
      </nav>
    </aside>
    <main class="system-content">
      <router-view />
    </main>
  </div>
</template>

<script setup lang="ts">
// Nav items are declared here (not in a separate config file) because
// the list is short and tightly coupled to which child routes exist.
// When a new child route is added in router/index.ts under /platform/system,
// add a matching entry here.
const navItems = [
  { name: 'systemAdmins',   label: '系统管理员', icon: 'user-shield' },
]
</script>

<style scoped>
.system-layout {
  display: flex;
  height: 100%;
  width: 100%;
  background: var(--td-bg-color-page, #f5f5f5);
}

.system-sidebar {
  width: 240px;
  flex-shrink: 0;
  background: var(--td-bg-color-container, #fff);
  border-right: 1px solid var(--td-border-level-1-color, #e7e7e7);
  padding: 20px 12px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.system-sidebar-header {
  padding: 0 12px 12px;
  border-bottom: 1px solid var(--td-border-level-1-color, #e7e7e7);
}

.system-sidebar-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary, #000);
}

.system-sidebar-subtitle {
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
  margin-top: 4px;
  letter-spacing: 0.5px;
}

.system-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.system-nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 14px;
  color: var(--td-text-color-primary, #333);
  transition: background-color 0.15s;
}

.system-nav-item:hover {
  background: var(--td-bg-color-component-hover, #f3f3f3);
}

.system-nav-item.active {
  background: var(--td-brand-color-light, #e0eaff);
  color: var(--td-brand-color, #0052d9);
  font-weight: 500;
}

.system-nav-icon {
  font-size: 16px;
}

.system-content {
  flex: 1;
  overflow: auto;
  padding: 24px 32px;
}
</style>
