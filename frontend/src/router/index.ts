import { createRouter, createWebHistory } from 'vue-router'
import { checkInitializationStatus } from '@/api/initialization'
import { useAuthStore } from '@/stores/auth'
import { validateToken } from '@/api/auth'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: "/",
      redirect: "/platform/knowledgeBase",
    },
    {
      path: "/login",
      name: "login",
      component: () => import("../views/auth/Login.vue"),
      meta: { requiresAuth: false, requiresInit: false }
    },
    {
      path: "/initialization",
      name: "initialization",
      component: () => import("../views/initialization/InitializationConfig.vue"),
      meta: { requiresInit: false } // 初始化页面不需要检查初始化状态
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
      redirect: "/platform/knowledgeBase",
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
          path: "knowledgeBase",
          name: "knowledgeBase",
          component: () => import("../views/knowledge/KnowledgeBase.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "creatChat",
          name: "creatChat",
          component: () => import("../views/creatChat/creatChat.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "chat/:chatid",
          name: "chat",
          component: () => import("../views/chat/index.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
            path: "settings",
            name: "settings",
            component: () => import("../views/settings/Settings.vue"),
          meta: { requiresInit: true }
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
      next('/platform/knowledgeBase')
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

  // 检查系统初始化状态
  if (to.meta.requiresInit !== false) {
    try {
      const { initialized } = await checkInitializationStatus()
      
      if (initialized) {
        // 系统已初始化，记录到本地存储并正常跳转
        localStorage.setItem('system_initialized', 'true')
        next()
      } else {
        // 系统未初始化，跳转到初始化页面
        next('/initialization')
      }
    } catch (error) {
      console.error('检查初始化状态失败:', error)
      // 如果是401，跳转登录，不再误导去初始化
      const status = (error as any)?.status
      if (status === 401) {
        next('/login')
        return
      }
      // 其他错误默认认为需要初始化
      next('/initialization')
    }
  } else {
    next()
  }
});

export default router
