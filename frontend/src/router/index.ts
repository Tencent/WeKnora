import { createRouter, createWebHistory } from 'vue-router'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { useAuthStore } from '@/stores/auth'
import { validateToken } from '@/api/auth'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: "/",
      redirect: "/platform/knowledge-bases",
    },
    {
      path: "/login",
      name: "login",
      component: () => import("../views/auth/Login.vue"),
      meta: { requiresAuth: false, requiresInit: false }
    },
    {
      path: "/knowledgeBase",
      name: "home",
      component: () => import("../views/knowledge/KnowledgeBase.vue"),
      meta: { requiresInit: true, requiresAuth: true }
    },
    {
      path: "/platform",
      name: "Platform",
      redirect: "/platform/knowledge-bases",
      component: () => import("../views/platform/index.vue"),
      meta: { requiresInit: true, requiresAuth: true },
      children: [
        {
          path: "tenant",
          name: "tenant",
          component: () => import("../views/tenant/TenantInfo.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases",
          name: "knowledgeBaseList",
          component: () => import("../views/knowledge/KnowledgeBaseList.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases/:kbId",
          name: "knowledgeBaseDetail",
          component: () => import("../views/knowledge/KnowledgeBase.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases/:kbId/creatChat",
          name: "kbCreatChat",
          component: () => import("../views/creatChat/creatChat.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases/:kbId/settings",
          name: "knowledgeBaseSettings",
          component: () => import("../views/initialization/InitializationContent.vue"),
          props: { isKbSettings: true },
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "chat/:kbId/:chatid",
          name: "chat",
          component: () => import("../views/chat/index.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
      ],
    },
  ],
});

// 路由守卫：检查认证状态和系统初始化状态
router.beforeEach(async (to, from, next) => {
  const authStore = useAuthStore()
  
  // 如果访问的是登录页面或初始化页面，直接放行
  if (to.meta.requiresAuth === false || to.meta.requiresInit === false) {
    // 如果已登录用户访问登录页面，重定向到知识库列表页面
    if (to.path === '/login' && authStore.isLoggedIn) {
      next('/platform/knowledge-bases')
      return
    }
    next()
    return
  }

  // 检查用户认证状态
  if (to.meta.requiresAuth !== false) {
    if (!authStore.isLoggedIn) {
      // 未登录，跳转到登录页面
      next('/login')
      return
    }

    // 验证Token有效性
    // try {
    //   const { valid } = await validateToken()
    //   if (!valid) {
    //     // Token无效，清空认证信息并跳转到登录页面
    //     authStore.logout()
    //     next('/login')
    //     return
    //   }
    // } catch (error) {
    //   console.error('Token验证失败:', error)
    //   authStore.logout()
    //   next('/login')
    //   return
    // }
  }

  next()
});

export default router
