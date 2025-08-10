import { createRouter, createWebHistory } from 'vue-router'
import { checkInitializationStatus } from '@/api/initialization'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: "/",
      redirect: "/platform",
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
      meta: { requiresInit: true }
    },
    {
      path: "/platform",
      name: "Platform",
      redirect: "/platform/knowledgeBase",
      component: () => import("../views/platform/index.vue"),
      meta: { requiresInit: true },
      children: [
        {
          path: "knowledgeBase",
          name: "knowledgeBase",
          component: () => import("../views/knowledge/KnowledgeBase.vue"),
          meta: { requiresInit: true }
        },
        {
          path: "creatChat",
          name: "creatChat",
          component: () => import("../views/creatChat/creatChat.vue"),
          meta: { requiresInit: true }
        },
        {
          path: "chat/:chatid",
          name: "chat",
          component: () => import("../views/chat/index.vue"),
          meta: { requiresInit: true }
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

// 路由守卫：检查系统初始化状态
router.beforeEach(async (to, from, next) => {
  // 如果访问的是初始化页面，直接放行
  if (to.meta.requiresInit === false) {
    next();
    return;
  }

1

  try {
    // 检查系统是否已初始化
    const { initialized } = await checkInitializationStatus();
    
    if (initialized) {
      // 系统已初始化，记录到本地存储并正常跳转
      localStorage.setItem('system_initialized', 'true');
      next();
    } else {
      // 系统未初始化，跳转到初始化页面
      console.log('系统未初始化，跳转到初始化页面');
      next('/initialization');
    }
  } catch (error) {
    console.error('检查初始化状态失败:', error);
    // 如果检查失败，默认认为需要初始化
    next('/initialization');
  }
});

export default router
