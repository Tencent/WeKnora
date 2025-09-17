<template>
  <div class="login-container">
    <!-- 登录表单 -->
    <div class="login-card" v-if="!isRegisterMode">
      <!-- 系统Logo和标题 -->
      <div class="login-header">
        <div class="logo">
          <img src="@/assets/img/weknora.png" alt="WeKnora" class="logo-img" />
        </div>
        <p class="login-subtitle">基于大模型的文档理解与语义检索框架</p>
      </div>

      <div class="login-form">
        <t-form
          ref="formRef"
          :data="formData"
          :rules="formRules"
          @submit="handleLogin"
          layout="vertical"
        >
          <t-form-item label="邮箱" name="email">
            <t-input
              v-model="formData.email"
              placeholder="请输入邮箱地址"
              type="email"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item label="密码" name="password">
            <t-input
              v-model="formData.password"
              placeholder="请输入密码（8-32位，包含字母和数字）"
              type="password"
              size="large"
              :disabled="loading"
              @keydown.enter="handleLogin"
            />
          </t-form-item>

          <t-button
            type="submit"
            theme="primary"
            size="large"
            block
            :loading="loading"
            class="login-button"
          >
            {{ loading ? '登录中...' : '登录' }}
          </t-button>
        </t-form>

        <!-- 注册链接 -->
        <div class="register-link">
          <span>还没有账号？</span>
          <a href="#" @click.prevent="toggleMode" class="register-btn">
            立即注册
          </a>
        </div>
      </div>
    </div>

    <!-- 注册表单 -->
    <div class="register-card" v-if="isRegisterMode">
      <div class="login-header">
        <h1 class="login-title">创建账号</h1>
        <p class="login-subtitle">注册后系统将为您创建专属租户</p>
      </div>

      <div class="login-form">
        <t-form
          ref="registerFormRef"
          :data="registerData"
          :rules="registerRules"
          @submit="handleRegister"
          layout="vertical"
        >
          <t-form-item label="用户名" name="username">
            <t-input
              v-model="registerData.username"
              placeholder="请输入用户名"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item label="邮箱" name="email">
            <t-input
              v-model="registerData.email"
              placeholder="请输入邮箱地址"
              type="email"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item label="密码" name="password">
            <t-input
              v-model="registerData.password"
              placeholder="请输入密码（8-32位，包含字母和数字）"
              type="password"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item label="确认密码" name="confirmPassword">
            <t-input
              v-model="registerData.confirmPassword"
              placeholder="请再次输入密码"
              type="password"
              size="large"
              :disabled="loading"
              @keydown.enter="handleRegister"
            />
          </t-form-item>

          <t-button
            type="submit"
            theme="primary"
            size="large"
            block
            :loading="loading"
            class="login-button"
          >
            {{ loading ? '注册中...' : '注册' }}
          </t-button>
        </t-form>

        <!-- 返回登录 -->
        <div class="register-link">
          <span>已有账号？</span>
          <a href="#" @click.prevent="toggleMode" class="register-btn">
            返回登录
          </a>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, nextTick, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { login, register } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

// 表单引用
const formRef = ref()
const registerFormRef = ref()

// 状态管理
const loading = ref(false)
const isRegisterMode = ref(false)


// 登录表单数据
const formData = reactive<{[key: string]: any}>({
  email: '',
  password: '',
})

// 注册表单数据
const registerData = reactive<{[key: string]: any}>({
  username: '',
  email: '',
  password: '',
  confirmPassword: ''
})

// 登录表单验证规则
const formRules = {
  email: [
    { required: true, message: '请输入邮箱地址', type: 'error' },
    { email: true, message: '请输入正确的邮箱格式', type: 'error' }
  ],
  password: [
    { required: true, message: '请输入密码', type: 'error' },
    { min: 8, message: '密码至少8位', type: 'error' },
    { max: 32, message: '密码不能超过32位', type: 'error' },
    { pattern: /[a-zA-Z]/, message: '密码必须包含字母', type: 'error' },
    { pattern: /\d/, message: '密码必须包含数字', type: 'error' }
  ]
}

// 注册表单验证规则
const registerRules = {
  username: [
    { required: true, message: '请输入用户名', type: 'error' },
    { min: 2, message: '用户名至少2位', type: 'error' },
    { max: 20, message: '用户名不能超过20位', type: 'error' },
    { 
      pattern: /^[a-zA-Z0-9_\u4e00-\u9fa5]+$/, 
      message: '用户名只能包含字母、数字、下划线和中文', 
      type: 'error' 
    }
  ],
  email: [
    { required: true, message: '请输入邮箱地址', type: 'error' },
    { email: true, message: '请输入正确的邮箱格式', type: 'error' }
  ],
  password: [
    { required: true, message: '请输入密码', type: 'error' },
    { min: 8, message: '密码至少8位', type: 'error' },
    { max: 32, message: '密码不能超过32位', type: 'error' },
    { pattern: /[a-zA-Z]/, message: '密码必须包含字母', type: 'error' },
    { pattern: /\d/, message: '密码必须包含数字', type: 'error' }
  ],
  confirmPassword: [
    { required: true, message: '请确认密码', type: 'error' },
    {
      validator: (val: string) => val === registerData.password,
      message: '两次输入的密码不一致',
      type: 'error'
    }
  ]
}

// 切换登录/注册模式
const toggleMode = () => {
  isRegisterMode.value = !isRegisterMode.value
  
  Object.keys(registerData).forEach(key => {
    (registerData as any)[key] = ''
  })
}

// 处理登录
const handleLogin = async () => {
  try {
    const valid = await formRef.value?.validate()
    if (!valid) return

    loading.value = true
    
    const response = await login({
      email: formData.email,
      password: formData.password,
    })

    if (response.success) {
      // 保存用户信息和token
      if (response.user && response.tenant && response.token) {
          authStore.setUser({
            id: response.user.id || '',
            username: response.user.username || '',
            email: response.user.email || '',
            avatar: response.user.avatar,
            tenant_id: String(response.tenant.id) || '',
            created_at: response.user.created_at || new Date().toISOString(),
            updated_at: response.user.updated_at || new Date().toISOString()
          })
          authStore.setToken(response.token)
          if (response.refresh_token) {
            authStore.setRefreshToken(response.refresh_token)
          }
          authStore.setTenant({
            id: String(response.tenant.id) || '',
            name: response.tenant.name || '',
            api_key: response.tenant.api_key || '',
            owner_id: response.user.id || '',
            created_at: response.tenant.created_at || new Date().toISOString(),
            updated_at: response.tenant.updated_at || new Date().toISOString()
          })
        }
      
      MessagePlugin.success('登录成功！')


      // 等待状态更新完成后再跳转
      await nextTick()
      router.replace('/platform/knowledge-bases')
    } else {
      MessagePlugin.error(response.message || '登录失败，请检查邮箱或密码')
    }
  } catch (error: any) {
    console.error('登录错误:', error)
    MessagePlugin.error(error.message || '登录失败，请稍后重试')
  } finally {
    loading.value = false
  }
}

// 处理注册
const handleRegister = async () => {
  try {
    const valid = await registerFormRef.value?.validate()
    if (!valid) return

    loading.value = true
    
    const response = await register({
      username: registerData.username,
      email: registerData.email,
      password: registerData.password
    })

    if (response.success) {
      MessagePlugin.success('注册成功！系统已为您创建专属租户，请登录使用')
      
      // 切换到登录模式并填入邮箱
      isRegisterMode.value = false
      formData.email = registerData.email
      
      // 清空注册表单
      Object.keys(registerData).forEach(key => {
        (registerData as any)[key] = ''
      })
    } else {
      MessagePlugin.error(response.message || '注册失败')
    }
  } catch (error: any) {
    console.error('注册错误:', error)
    MessagePlugin.error(error.message || '注册失败，请稍后重试')
  } finally {
    loading.value = false
  }
}

// 处理忘记密码
const handleForgotPassword = () => {
  MessagePlugin.info('忘记密码功能暂未开放，请联系管理员')
}

// 检查是否已登录
onMounted(() => {
  if (authStore.isLoggedIn) {
    router.replace('/platform/tenant/knowledge-bases')
  }
})
</script>

<style lang="less" scoped>
.login-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%);
  padding: 20px;
  box-sizing: border-box;
}

.login-card,
.register-card {
  width: 100%;
  max-width: 440px;
  background: #fff;
  border-radius: 14px;
  box-shadow: 0 10px 16px 0 #0000000f, 0 20px 24px -2px #0000001a;
  padding: 40px;
  box-sizing: border-box;
  animation: fadeInUp .28s ease-out both;
}

.login-header {
  text-align: center;
  margin-bottom: 32px;

  .logo {
    margin-bottom: 16px;

    .logo-img {
      width: 180px;
      height: auto;
      border-radius: 12px;
    }
  }

  .login-title {
    font-size: 28px;
    font-weight: 600;
    color: #000000e6;
    margin: 0 0 8px 0;
    font-family: "PingFang SC";
  }

  .login-subtitle {
    font-size: 16px;
    color: #0000008c;
    margin: 0;
    font-family: "PingFang SC";
  }
}

.login-form {
  :deep(.t-form-item__label) {
    font-size: 14px;
    color: #000000e6;
    font-weight: 500;
    margin-bottom: 8px;
    font-family: "PingFang SC";
    display: block;
    text-align: left;
  }

  :deep(.t-input) {
    border: 1px solid #E7E7E7;
    border-radius: 8px;
    background: #fff;
    
    &:focus-within {
      border-color: #07C05F;
      box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.1);
    }
    
    &:hover {
      border-color: #07C05F;
    }
    
    .t-input__inner {
      border: none !important;
      box-shadow: none !important;
      outline: none !important;
      background: transparent;
      font-size: 16px;
      font-family: "PingFang SC";
      
      &:focus {
        border: none !important;
        box-shadow: none !important;
        outline: none !important;
      }
    }
    
    .t-input__wrap {
      border: none !important;
      box-shadow: none !important;
    }
  }

  :deep(.t-form-item) {
    margin-bottom: 20px;
    
    &:last-child {
      margin-bottom: 0;
    }
  }
  
  :deep(.t-form-item__control) {
    width: 100%;
  }
}

.login-options {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 16px 0 24px 0;
  width: 100%;

  :deep(.t-checkbox) {
    display: flex;
    align-items: center;
    
    .t-checkbox__input {
      margin-right: 8px;
    }
  }

  :deep(.t-checkbox__label) {
    font-size: 14px;
    color: #00000099;
    font-family: "PingFang SC";
    line-height: 1.4;
    margin-left: 0;
  }

  .forgot-password {
    font-size: 14px;
    color: #07C05F;
    text-decoration: none;
    font-family: "PingFang SC";
    line-height: 1.4;

    &:hover {
      text-decoration: underline;
    }
  }
}

.login-button {
  height: 48px;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 500;
  font-family: "PingFang SC";
  margin: 16px 0 8px 0;

  :deep(.t-button) {
    background-color: #07C05F;
    border-color: #07C05F;

    &:hover {
      background-color: #06a855;
      border-color: #06a855;
    }
  }
}

.register-link {
  text-align: center;
  font-size: 14px;
  color: #00000099;
  font-family: "PingFang SC";

  .register-btn {
    color: #07C05F;
    text-decoration: none;
    margin-left: 4px;

    &:hover {
      text-decoration: underline;
    }
  }
}

// 响应式设计
@media (max-width: 480px) {
  .login-container {
    padding: 16px;
  }

  .login-card,
  .register-card {
    padding: 28px;
  }

  .login-header {
    margin-bottom: 24px;

    .login-title {
      font-size: 24px;
    }
  }
}

@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translate3d(0, 6px, 0);
  }
  to {
    opacity: 1;
    transform: translate3d(0, 0, 0);
  }
}
</style>