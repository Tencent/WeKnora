import { createRouter, createWebHashHistory, RouteRecordRaw } from "vue-router";

// NOTE:
// 1. This router is scoped to the feature bundle for preview/demo purposes.
// 2. Integrators can mount the views into the main application's router by
//    importing the page components (e.g. `TagManagement.vue`) directly and
//    wiring them to existing layouts/route hierarchies. See `README.md` for
//    migration examples.

const routes: RouteRecordRaw[] = [
  {
    path: "/",
    redirect: "/tags",
  },
  {
    path: "/tags",
    component: () => import("@views/TagManagement.vue"),
  },
  {
    path: "/virtual-kbs",
    component: () => import("@views/VirtualKBManagement.vue"),
  },
  {
    path: "/search",
    component: () => import("@views/EnhancedSearch.vue"),
  },
];

export const createVirtualKBRouter = () =>
  createRouter({
    history: createWebHashHistory(),
    routes,
  });
