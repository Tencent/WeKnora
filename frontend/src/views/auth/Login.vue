<template>
  <div class="login-layout">
    <!-- Global Animated Background - Knowledge Graph -->
    <div class="animated-bg">
      <!-- Knowledge Nodes with Icons -->
      <div class="knowledge-node node-1">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/>
          <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>
        </svg>
        </div>
      <div class="knowledge-node node-2">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
        </svg>
      </div>
      <div class="knowledge-node node-3">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <!-- Vector embedding points -->
          <circle cx="6" cy="6" r="1.5" fill="currentColor"/>
          <circle cx="12" cy="5" r="1.5" fill="currentColor"/>
          <circle cx="18" cy="7" r="1.5" fill="currentColor"/>
          <circle cx="5" cy="12" r="1.5" fill="currentColor"/>
          <circle cx="12" cy="12" r="2" fill="currentColor"/>
          <circle cx="19" cy="13" r="1.5" fill="currentColor"/>
          <circle cx="7" cy="18" r="1.5" fill="currentColor"/>
          <circle cx="13" cy="19" r="1.5" fill="currentColor"/>
          <circle cx="18" cy="18" r="1.5" fill="currentColor"/>
          <!-- Connection lines -->
          <line x1="6" y1="6" x2="12" y2="5" stroke-width="1"/>
          <line x1="12" y1="5" x2="18" y2="7" stroke-width="1"/>
          <line x1="6" y1="6" x2="5" y2="12" stroke-width="1"/>
          <line x1="12" y1="5" x2="12" y2="12" stroke-width="1"/>
          <line x1="18" y1="7" x2="19" y2="13" stroke-width="1"/>
          <line x1="5" y1="12" x2="12" y2="12" stroke-width="1"/>
          <line x1="12" y1="12" x2="19" y2="13" stroke-width="1"/>
          <line x1="5" y1="12" x2="7" y2="18" stroke-width="1"/>
          <line x1="12" y1="12" x2="13" y2="19" stroke-width="1"/>
          <line x1="19" y1="13" x2="18" y2="18" stroke-width="1"/>
          <line x1="7" y1="18" x2="13" y2="19" stroke-width="1"/>
          <line x1="13" y1="19" x2="18" y2="18" stroke-width="1"/>
        </svg>
      </div>
      <div class="knowledge-node node-4">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2L2 7l10 5 10-5-10-5z"/>
          <path d="M2 17l10 5 10-5"/>
          <path d="M2 12l10 5 10-5"/>
        </svg>
      </div>
      <div class="knowledge-node node-5">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
        </svg>
      </div>
      <div class="knowledge-node node-6">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M12 1v6m0 6v6M5.64 5.64l4.24 4.24m4.24 4.24l4.24 4.24M1 12h6m6 0h6M5.64 18.36l4.24-4.24m4.24-4.24l4.24-4.24"/>
        </svg>
      </div>
      <div class="knowledge-node node-7">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M9 11l3 3L22 4"/>
          <path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
        </svg>
      </div>
      <div class="knowledge-node node-8">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <ellipse cx="12" cy="5" rx="9" ry="3"/>
          <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
          <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
        </svg>
      </div>
      <div class="knowledge-node node-9">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2L2 7l10 5 10-5-10-5z"/>
          <path d="M2 17l10 5 10-5"/>
          <path d="M2 12l10 5 10-5"/>
        </svg>
      </div>
      <div class="knowledge-node node-10">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
          <polyline points="14 2 14 8 20 8"/>
        </svg>
      </div>
      <div class="knowledge-node node-11">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <path d="m21 21-4.35-4.35"/>
        </svg>
      </div>
      <div class="knowledge-node node-12">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
          <polyline points="3.27 6.96 12 12.01 20.73 6.96"/>
          <line x1="12" y1="22.08" x2="12" y2="12"/>
        </svg>
      </div>
      <div class="knowledge-node node-13">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 20h9"/>
          <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
        </svg>
      </div>
      <div class="knowledge-node node-14">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/>
          <line x1="3" y1="9" x2="21" y2="9"/>
          <line x1="9" y1="21" x2="9" y2="9"/>
        </svg>
      </div>
      <div class="knowledge-node node-15">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
        </svg>
      </div>
      <div class="knowledge-node node-16">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>
        </svg>
      </div>
      <div class="knowledge-node node-17">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
        </svg>
      </div>
      <div class="knowledge-node node-18">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
        </svg>
      </div>
      <div class="knowledge-node node-19">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>
        </svg>
      </div>
      <div class="knowledge-node node-20">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
          <circle cx="12" cy="7" r="4"/>
        </svg>
      </div>

      <!-- Connection Lines -->
      <svg class="knowledge-lines" viewBox="0 0 100 100" preserveAspectRatio="none">
        <!-- Horizontal connections -->
        <line class="connection-line line-1" x1="20" y1="15" x2="35" y2="25" />
        <line class="connection-line line-2" x1="35" y1="25" x2="55" y2="20" />
        <line class="connection-line line-3" x1="55" y1="20" x2="65" y2="15" />
        <line class="connection-line line-4" x1="65" y1="15" x2="85" y2="12" />
        
        <!-- Middle layer connections -->
        <line class="connection-line line-5" x1="8" y1="35" x2="25" y2="45" />
        <line class="connection-line line-6" x1="25" y1="45" x2="45" y2="50" />
        <line class="connection-line line-7" x1="45" y1="50" x2="65" y2="48" />
        <line class="connection-line line-8" x1="65" y1="48" x2="72" y2="42" />
        <line class="connection-line line-9" x1="72" y1="42" x2="90" y2="38" />
        
        <!-- Lower connections -->
        <line class="connection-line line-10" x1="10" y1="55" x2="20" y2="60" />
        <line class="connection-line line-11" x1="20" y1="60" x2="40" y2="70" />
        <line class="connection-line line-12" x1="40" y1="70" x2="60" y2="75" />
        <line class="connection-line line-13" x1="60" y1="75" x2="75" y2="80" />
        
        <!-- Vertical connections -->
        <line class="connection-line line-14" x1="20" y1="15" x2="20" y2="60" />
        <line class="connection-line line-15" x1="35" y1="25" x2="25" y2="45" />
        <line class="connection-line line-16" x1="55" y1="20" x2="45" y2="50" />
        <line class="connection-line line-17" x1="75" y1="30" x2="65" y2="48" />
        <line class="connection-line line-18" x1="40" y1="70" x2="12" y2="68" />
        <line class="connection-line line-19" x1="60" y1="75" x2="80" y2="65" />
        <line class="connection-line line-20" x1="82" y1="52" x2="90" y2="38" />
      </svg>
    </div>

    <!-- Logo - Top Left -->
    <a href="https://github.com/Tencent/WeKnora" target="_blank" class="header-logo" :title="$t('common.github')">
      <img src="@/assets/img/weknora.png" alt="WeKnora" class="logo-image" />
    </a>

    <!-- Header Links - Top Right -->
    <div class="header-links">
      <a href="https://weknora.weixin.qq.com" target="_blank" class="header-link" :title="$t('common.website')">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round">
          <circle cx="12" cy="12" r="10"/>
          <line x1="2" y1="12" x2="22" y2="12"/>
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        </svg>
        <span class="link-text">{{ $t('common.website') }}</span>
      </a>
      
      <a href="https://github.com/Tencent/WeKnora" target="_blank" class="header-link" :title="$t('common.info')">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z"/>
        </svg>
        <span class="link-text">GitHub</span>
      </a>
      
      <div class="language-switch">
        <button @click="toggleLanguageMenu" class="header-link" :title="languageOptions.find(l => l.value === currentLanguage)?.label">
          <span class="lang-flag-icon">{{ languageOptions.find(l => l.value === currentLanguage)?.flag }}</span>
          <span class="link-text">{{ languageOptions.find(l => l.value === currentLanguage)?.shortLabel }}</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </button>
        
        <!-- Language Dropdown -->
        <div v-if="showLanguageMenu" class="language-dropdown">
          <div 
            v-for="lang in languageOptions" 
            :key="lang.value"
            @click="selectLanguage(lang.value)"
            class="language-option"
            :class="{ active: currentLanguage === lang.value }"
          >
            <span class="lang-flag">{{ lang.flag }}</span>
            <span class="lang-label">{{ lang.label }}</span>
            <span v-if="currentLanguage === lang.value" class="check-icon">✓</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Left Showcase Section -->
    <div class="showcase-section">
      <div class="showcase-content">
        <p class="showcase-subtitle">{{ $t('platform.subtitle') }}</p>
        <p class="showcase-description">{{ $t('platform.description') }}</p>

        <div class="feature-tags">
          <span class="tag">{{ $t('platform.rag') }}</span>
          <span class="tag">{{ $t('platform.hybridSearch') }}</span>
          <span class="tag">{{ $t('platform.localDeploy') }}</span>
        </div>

        <!-- Swiper Carousel -->
        <div class="carousel-container">
          <swiper
            :modules="modules"
            :slides-per-view="1"
            :loop="true"
            :autoplay="{
              delay: 4000,
              disableOnInteraction: false,
            }"
            :effect="'fade'"
            :fade-effect="{ crossFade: true }"
            :pagination="{ clickable: true, dynamicBullets: false }"
            :speed="800"
            class="screenshot-swiper"
          >
            <swiper-slide v-for="(slide, index) in slides" :key="index">
              <div class="slide-content">
                <img :src="slide.image" :alt="slide.title" class="slide-image" />
              </div>
            </swiper-slide>
          </swiper>
        </div>
      </div>
    </div>

    <!-- Right Form Section -->
    <div class="form-section">
      <div class="form-panel">
        <!-- Login Card -->
        <div class="form-card" v-if="!isRegisterMode">
                <div class="form-header">
                  <h2 class="form-title">{{ $t('auth.login') }}</h2>
                  <p class="form-welcome">{{ $t('auth.subtitle') }}</p>
                </div>

          <div class="form-content">
        <t-form
          ref="formRef"
          :data="formData"
          :rules="formRules"
          @submit="handleLogin"
          layout="vertical"
        >
          <t-form-item :label="$t('auth.email')" name="email">
            <t-input
              v-model="formData.email"
              :placeholder="$t('auth.emailPlaceholder')"
              type="email"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item :label="$t('auth.password')" name="password">
            <t-input
              v-model="formData.password"
              :placeholder="$t('auth.passwordPlaceholder')"
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
                class="submit-button"
          >
                {{ loading ? $t('auth.loggingIn') : $t('auth.login') }}
          </t-button>
        </t-form>

            <div class="form-footer">
          <span>{{ $t('auth.noAccount') }}</span>
              <a href="#" @click.prevent="toggleMode" class="link-button">
            {{ $t('auth.registerNow') }}
          </a>
        </div>

            <!-- Features list -->
            <div class="login-features">
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.multimodalParsing') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.hybridSearchEngine') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.ragQandA') }}</span>
              </div>
            </div>
      </div>
    </div>

        <!-- Register Card -->
        <div class="form-card" v-if="isRegisterMode">
          <div class="form-header">
            <h2 class="form-title">{{ $t('auth.createAccount') }}</h2>
            <p class="form-subtitle">{{ $t('auth.registerSubtitle') }}</p>
      </div>

          <div class="form-content">
        <t-form
          ref="registerFormRef"
          :data="registerData"
          :rules="registerRules"
          @submit="handleRegister"
          layout="vertical"
        >
          <t-form-item :label="$t('auth.username')" name="username">
            <t-input
              v-model="registerData.username"
              :placeholder="$t('auth.usernamePlaceholder')"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item :label="$t('auth.email')" name="email">
            <t-input
              v-model="registerData.email"
              :placeholder="$t('auth.emailPlaceholder')"
              type="email"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item :label="$t('auth.password')" name="password">
            <t-input
              v-model="registerData.password"
              :placeholder="$t('auth.passwordPlaceholder')"
              type="password"
              size="large"
              :disabled="loading"
            />
          </t-form-item>

          <t-form-item :label="$t('auth.confirmPassword')" name="confirmPassword">
            <t-input
              v-model="registerData.confirmPassword"
              :placeholder="$t('auth.confirmPasswordPlaceholder')"
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
                class="submit-button"
          >
            {{ loading ? $t('auth.registering') : $t('auth.register') }}
          </t-button>
        </t-form>

            <div class="form-footer">
          <span>{{ $t('auth.haveAccount') }}</span>
              <a href="#" @click.prevent="toggleMode" class="link-button">
            {{ $t('auth.backToLogin') }}
          </a>
        </div>

            <!-- Features list for register -->
            <div class="login-features">
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.independentTenant') }}</span>
      </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.fullApiAccess') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.knowledgeBaseManagement') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, nextTick, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { Swiper, SwiperSlide } from 'swiper/vue'
import { Autoplay, EffectFade, Pagination } from 'swiper/modules'
import 'swiper/css'
import 'swiper/css/effect-fade'
import 'swiper/css/pagination'
import { login, register } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'

// Import screenshot images
import screenshot1 from '@/assets/img/screenshot-1.svg'
import screenshot2 from '@/assets/img/screenshot-2.svg'
import screenshot4 from '@/assets/img/screenshot-4.svg'

const router = useRouter()
const authStore = useAuthStore()
const { t, locale } = useI18n()

// Swiper modules
const modules = [Autoplay, EffectFade, Pagination]

// Carousel slides data
const slides = [
  {
    image: screenshot4,
    title: t('platform.carousel.agenticRagTitle'),
    description: t('platform.carousel.agenticRagDesc')
  },
  {
    image: screenshot2,
    title: t('platform.carousel.hybridSearchTitle'),
    description: t('platform.carousel.hybridSearchDesc')
  },
  {
    image: screenshot1,
    title: t('platform.carousel.smartDocRetrievalTitle'),
    description: t('platform.carousel.smartDocRetrievalDesc')
  }
]

// Form references
const formRef = ref()
const registerFormRef = ref()

// State management
const loading = ref(false)
const isRegisterMode = ref(false)
const showLanguageMenu = ref(false)

// Language options
const languageOptions = [
  { value: 'zh-CN', label: '简体中文', shortLabel: '中文', flag: '🇨🇳' },
  { value: 'en-US', label: 'English', shortLabel: 'EN', flag: '🇺🇸' },
  { value: 'ru-RU', label: 'Русский', shortLabel: 'RU', flag: '🇷🇺' },
  { value: 'ko-KR', label: '한국어', shortLabel: '한국어', flag: '🇰🇷' }
]

// Current language computed from i18n
const currentLanguage = computed(() => locale.value)

// Login form data
const formData = reactive<{[key: string]: any}>({
  email: '',
  password: '',
})

// Register form data
const registerData = reactive<{[key: string]: any}>({
  username: '',
  email: '',
  password: '',
  confirmPassword: ''
})

// Login form validation rules
const formRules = computed(() => ({
  email: [
    { required: true, message: t('auth.emailRequired'), type: 'error' },
    { email: true, message: t('auth.emailInvalid'), type: 'error' }
  ],
  password: [
    { required: true, message: t('auth.passwordRequired'), type: 'error' },
    { min: 8, message: t('auth.passwordMinLength'), type: 'error' },
    { max: 32, message: t('auth.passwordMaxLength'), type: 'error' },
    { pattern: /[a-zA-Z]/, message: t('auth.passwordMustContainLetter'), type: 'error' },
    { pattern: /\d/, message: t('auth.passwordMustContainNumber'), type: 'error' }
  ]
}))

// Register form validation rules
const registerRules = computed(() => ({
  username: [
    { required: true, message: t('auth.usernameRequired'), type: 'error' },
    { min: 2, message: t('auth.usernameMinLength'), type: 'error' },
    { max: 20, message: t('auth.usernameMaxLength'), type: 'error' },
    { 
      pattern: /^[a-zA-Z0-9_\u4e00-\u9fa5]+$/, 
      message: t('auth.usernameInvalid'), 
      type: 'error' 
    }
  ],
  email: [
    { required: true, message: t('auth.emailRequired'), type: 'error' },
    { email: true, message: t('auth.emailInvalid'), type: 'error' }
  ],
  password: [
    { required: true, message: t('auth.passwordRequired'), type: 'error' },
    { min: 8, message: t('auth.passwordMinLength'), type: 'error' },
    { max: 32, message: t('auth.passwordMaxLength'), type: 'error' },
    { pattern: /[a-zA-Z]/, message: t('auth.passwordMustContainLetter'), type: 'error' },
    { pattern: /\d/, message: t('auth.passwordMustContainNumber'), type: 'error' }
  ],
  confirmPassword: [
    { required: true, message: t('auth.confirmPasswordRequired'), type: 'error' },
    {
      validator: (val: string) => val === registerData.password,
      message: t('auth.passwordMismatch'),
      type: 'error'
    }
  ]
}))

// Toggle login/register mode
const toggleMode = () => {
  isRegisterMode.value = !isRegisterMode.value
  
  Object.keys(registerData).forEach(key => {
    (registerData as any)[key] = ''
  })
}

// Toggle language menu
const toggleLanguageMenu = () => {
  showLanguageMenu.value = !showLanguageMenu.value
}

// Select language
const selectLanguage = (lang: string) => {
  locale.value = lang
  localStorage.setItem('locale', lang)
  showLanguageMenu.value = false
  MessagePlugin.success(t('language.languageSaved'))
}

// Close language menu when clicking outside
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('.language-switch')) {
    showLanguageMenu.value = false
  }
}

// Add click outside listener
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

// Clean up listener
import { onBeforeUnmount } from 'vue'
onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})

// Handle login
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
      // Save user info and token
      if (response.user && response.tenant && response.token) {
          authStore.setUser({
            id: response.user.id || '',
            username: response.user.username || '',
            email: response.user.email || '',
            avatar: response.user.avatar,
            tenant_id: String(response.tenant.id) || '',
            can_access_all_tenants: response.user.can_access_all_tenants || false,
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
      
      MessagePlugin.success(t('auth.loginSuccess'))

      // Wait for state update before redirect
      await nextTick()
      router.replace('/platform/knowledge-bases')
    } else {
      MessagePlugin.error(response.message || t('auth.loginError'))
    }
  } catch (error: any) {
    console.error('登录错误:', error)
    MessagePlugin.error(error.message || t('auth.loginErrorRetry'))
  } finally {
    loading.value = false
  }
}

// Handle registration
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
      MessagePlugin.success(t('auth.registerSuccess'))
      
      // Switch to login mode and fill in email
      isRegisterMode.value = false
      formData.email = registerData.email
      
      // Clear register form
      Object.keys(registerData).forEach(key => {
        (registerData as any)[key] = ''
      })
    } else {
      MessagePlugin.error(response.message || t('auth.registerFailed'))
    }
  } catch (error: any) {
    console.error('注册错误:', error)
    MessagePlugin.error(error.message || t('auth.registerError'))
  } finally {
    loading.value = false
  }
}

// Check if already logged in
onMounted(() => {
  if (authStore.isLoggedIn) {
    router.replace('/platform/tenant/knowledge-bases')
  }
})
</script>

<style lang="less" scoped>
.login-layout {
  display: flex;
  width: 100%;
  min-height: 100vh;
  overflow: hidden;
  position: relative;
  background: linear-gradient(225deg, #022c22 0%, #064e3b 15%, #065f46 25%, #047857 38%, #059669 50%, #07C05F 65%, #10B981 78%, #34D399 90%, #6EE7B7 100%);

  &::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: radial-gradient(circle at 20% 50%, rgba(255, 255, 255, 0.06) 0%, transparent 50%),
                radial-gradient(circle at 80% 50%, rgba(255, 255, 255, 0.04) 0%, transparent 50%);
    pointer-events: none;
  }
}

/* Global Animated Background - Knowledge Graph */
.animated-bg {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 1;
  overflow: hidden;
}

/* Knowledge Nodes */
.knowledge-node {
  position: absolute;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.15);
  backdrop-filter: blur(4px);
  border: 2px solid rgba(255, 255, 255, 0.3);
  box-shadow: 
    0 0 15px rgba(255, 255, 255, 0.4),
    0 0 30px rgba(16, 185, 129, 0.3),
    inset 0 0 15px rgba(255, 255, 255, 0.1);
  display: flex;
  align-items: center;
  justify-content: center;
  animation: nodePulse 4s infinite ease-in-out;
  transition: all 0.3s ease;
}

.knowledge-node:hover {
  transform: scale(1.2);
  box-shadow: 
    0 0 20px rgba(255, 255, 255, 0.6),
    0 0 40px rgba(16, 185, 129, 0.5),
    inset 0 0 20px rgba(255, 255, 255, 0.2);
}

.node-icon {
  width: 20px;
  height: 20px;
  color: rgba(255, 255, 255, 0.9);
  filter: drop-shadow(0 0 3px rgba(16, 185, 129, 0.5));
}

.node-1 {
  top: 15%;
  left: 20%;
  animation-delay: 0s;
}

.node-2 {
  top: 25%;
  left: 35%;
  animation-delay: 0.5s;
}

.node-3 {
  top: 20%;
  left: 55%;
  animation-delay: 1s;
}

.node-4 {
  top: 30%;
  left: 75%;
  animation-delay: 1.5s;
}

.node-5 {
  top: 45%;
  left: 25%;
  animation-delay: 2s;
}

.node-6 {
  top: 50%;
  left: 45%;
  animation-delay: 2.5s;
}

.node-7 {
  top: 48%;
  left: 65%;
  animation-delay: 3s;
}

.node-8 {
  top: 60%;
  left: 20%;
  animation-delay: 0.3s;
}

.node-9 {
  top: 70%;
  left: 40%;
  animation-delay: 0.8s;
}

.node-10 {
  top: 65%;
  left: 80%;
  animation-delay: 1.3s;
}

.node-11 {
  top: 12%;
  right: 15%;
  animation-delay: 1.8s;
}

.node-12 {
  top: 38%;
  right: 10%;
  animation-delay: 2.3s;
}

.node-13 {
  top: 55%;
  left: 10%;
  animation-delay: 1.1s;
}

.node-14 {
  top: 35%;
  left: 8%;
  animation-delay: 2.8s;
}

.node-15 {
  top: 75%;
  left: 60%;
  animation-delay: 1.6s;
}

.node-16 {
  top: 80%;
  right: 25%;
  animation-delay: 3.2s;
}

.node-17 {
  top: 15%;
  right: 35%;
  animation-delay: 2.1s;
}

.node-18 {
  top: 42%;
  right: 28%;
  animation-delay: 0.6s;
}

.node-19 {
  top: 68%;
  left: 12%;
  animation-delay: 1.9s;
}

.node-20 {
  top: 52%;
  right: 18%;
  animation-delay: 2.6s;
}

@keyframes nodePulse {
  0%, 100% {
    transform: scale(1);
    opacity: 0.7;
  }
  50% {
    transform: scale(1.08);
    opacity: 0.9;
  }
}

/* Connection Lines */
.knowledge-lines {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  opacity: 0.35;
}

.connection-line {
  stroke: rgba(255, 255, 255, 0.5);
  stroke-width: 1.5;
  stroke-dasharray: 6, 3;
  stroke-linecap: round;
  animation: lineFlow 10s infinite linear;
  filter: drop-shadow(0 0 3px rgba(16, 185, 129, 0.4));
}

.line-1 { animation-delay: 0s; }
.line-2 { animation-delay: 0.5s; }
.line-3 { animation-delay: 1s; }
.line-4 { animation-delay: 0.3s; }
.line-5 { animation-delay: 0.8s; }
.line-6 { animation-delay: 1.3s; }
.line-7 { animation-delay: 1.8s; }
.line-8 { animation-delay: 2.3s; }
.line-9 { animation-delay: 0.2s; }
.line-10 { animation-delay: 0.7s; }
.line-11 { animation-delay: 1.2s; }
.line-12 { animation-delay: 0.6s; }
.line-13 { animation-delay: 0.4s; }
.line-14 { animation-delay: 1.1s; }
.line-15 { animation-delay: 0.9s; }
.line-16 { animation-delay: 1.5s; }
.line-17 { animation-delay: 2.1s; }
.line-18 { animation-delay: 1.7s; }
.line-19 { animation-delay: 0.35s; }
.line-20 { animation-delay: 1.4s; }

@keyframes lineFlow {
  0% {
    stroke-dashoffset: 0;
  }
  100% {
    stroke-dashoffset: 18;
  }
}

/* Left Showcase Section */
.showcase-section {
  flex: 0 0 52%;
  display: flex;
  align-items: flex-end;
  padding: 100px 30px 100px 50px;
  box-sizing: border-box;
  position: relative;
}

.showcase-content {
  width: 100%;
  max-width: 600px;
  position: relative;
  z-index: 2;
  display: flex;
  flex-direction: column;
  margin-bottom: 60px;
}

.showcase-subtitle {
  margin-top: 0;
  font-size: 22px;
  color: rgba(255, 255, 255, 0.95);
  margin: 0 0 8px 0;
  font-family: "PingFang SC", sans-serif;
  line-height: 1.4;
  font-weight: 500;
}

.showcase-description {
  font-size: 15px;
  color: rgba(255, 255, 255, 0.8);
  margin: 0 0 28px 0;
  font-family: "PingFang SC", sans-serif;
  line-height: 1.5;
}

.feature-tags {
  display: flex;
  gap: 12px;
  margin-bottom: 40px;
  flex-wrap: wrap;
}

.tag {
  display: inline-block;
  padding: 8px 20px;
  background: rgba(255, 255, 255, 0.2);
  backdrop-filter: blur(10px);
  border-radius: 20px;
  color: #ffffff;
  font-size: 14px;
  font-weight: 500;
  font-family: "PingFang SC", sans-serif;
}

/* Carousel */
.carousel-container {
  width: 100%;
  margin-top: 48px;
}

.screenshot-swiper {
  width: 100%;
  border-radius: 16px;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  padding-bottom: 40px;

  :deep(.swiper-wrapper) {
    transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.swiper-pagination) {
    bottom: 15px !important;
    z-index: 10;
  }

  :deep(.swiper-pagination-bullet) {
    width: 10px;
    height: 10px;
    background: rgba(255, 255, 255, 0.5);
    opacity: 1;
    transition: all 0.3s ease;
    margin: 0 6px !important;
  }

  :deep(.swiper-pagination-bullet-active) {
    background: #ffffff;
    width: 28px;
    border-radius: 5px;
  }
}

.slide-content {
  width: 100%;
  height: 100%;
  background: #ffffff;
  border-radius: 16px;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
}

.slide-image {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
}

/* Right Form Section */
.form-section {
  flex: 0 0 48%;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding: 40px 50px 100px 30px;
  box-sizing: border-box;
  position: relative;
}

.form-panel {
  width: 100%;
  max-width: 480px;
  margin-bottom: 60px;
  position: relative;
  z-index: 2;
}

.header-logo {
  position: fixed;
  top: 32px;
  left: 50px;
  z-index: 100;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  cursor: pointer;

  &:hover {
    transform: translateY(-2px);
  }

  .logo-image {
    width: 120px;
      height: auto;
    filter: brightness(1.1) contrast(1.05) drop-shadow(0 4px 12px rgba(0, 0, 0, 0.25));
    transition: all 0.3s ease;
  }

  &:hover .logo-image {
    filter: brightness(1.15) contrast(1.08) drop-shadow(0 6px 16px rgba(0, 0, 0, 0.3));
  }
}

.header-links {
  position: fixed;
  top: 28px;
  right: 28px;
  display: flex;
  align-items: center;
  gap: 10px;
  z-index: 100;
}

.header-link {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 9px 15px;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.2);
  backdrop-filter: blur(8px);
  border: 1px solid rgba(255, 255, 255, 0.25);
  color: #ffffff;
  text-decoration: none;
  font-size: 13px;
    font-weight: 600;
  font-family: "PingFang SC", sans-serif;
  letter-spacing: 0.2px;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  cursor: pointer;
  position: relative;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);

  svg {
    flex-shrink: 0;
    transition: transform 0.25s ease;
  }

  .link-text {
    line-height: 1;
  }

  &:hover {
    background: rgba(255, 255, 255, 0.3);
    border-color: rgba(255, 255, 255, 0.4);
    color: #ffffff;
    transform: translateY(-2px);
    box-shadow: 
      0 4px 16px rgba(0, 0, 0, 0.15),
      0 0 0 1px rgba(255, 255, 255, 0.2);

    svg {
      transform: scale(1.08);
    }
  }

  &:active {
    transform: translateY(-1px);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
  }
}

.language-switch {
  position: relative;

  button {
    background: rgba(255, 255, 255, 0.2);
    border: 1px solid rgba(255, 255, 255, 0.25);
    backdrop-filter: blur(8px);
    color: #ffffff;

    .lang-flag-icon {
    font-size: 16px;
      line-height: 1;
      flex-shrink: 0;
    }

    &:hover {
      background: rgba(255, 255, 255, 0.3);
      border-color: rgba(255, 255, 255, 0.4);
      color: #ffffff;
      box-shadow: 
        0 4px 16px rgba(0, 0, 0, 0.15),
        0 0 0 1px rgba(255, 255, 255, 0.2);
    }

    svg:last-child {
      margin-left: 2px;
      flex-shrink: 0;
      transition: transform 0.25s ease;
    }

    &:hover svg:last-child {
      transform: translateY(2px);
    }
  }
}

.language-dropdown {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  min-width: 160px;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(14px);
  border: 1px solid #E5E7EB;
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  overflow: hidden;
  z-index: 1000;
  animation: dropdownFadeIn 0.2s ease-out;
}

.language-option {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
  transition: all 0.2s;
  font-size: 13px;
  font-family: "PingFang SC", sans-serif;
  color: #374151;

  .lang-flag {
    font-size: 16px;
    flex-shrink: 0;
  }

  .lang-label {
    flex: 1;
  }

  .check-icon {
    color: #10B981;
    font-weight: 700;
    font-size: 14px;
    flex-shrink: 0;
  }

  &:hover {
    background: #F3F4F6;
  }

  &.active {
    background: #F0FDF4;
    color: #059669;
  }
}

@keyframes dropdownFadeIn {
  from {
    opacity: 0;
    transform: translateY(-8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.form-card {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  padding: 40px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.15), 0 0 0 1px rgba(255, 255, 255, 0.3);
  box-sizing: border-box;
  animation: slideInRight 0.4s ease-out;
  border: none;
  width: 100%;
}

.form-header {
  text-align: center;
  margin-bottom: 32px;
}

.form-title {
  font-size: 24px;
    font-weight: 600;
  color: #111827;
  margin: 0 0 6px 0;
  font-family: "PingFang SC", sans-serif;
}

.form-welcome {
  font-size: 13px;
  color: #6B7280;
    margin: 0;
  font-family: "PingFang SC", sans-serif;
}

.form-subtitle {
  font-size: 13px;
  color: #6B7280;
  margin: 0;
  font-family: "PingFang SC", sans-serif;
}

.form-content {
  :deep(.t-form-item__label) {
    font-size: 14px;
    color: #111827;
    font-weight: 500;
    margin-bottom: 8px;
    font-family: "PingFang SC", sans-serif;
    display: block;
    text-align: left;
  }

  :deep(.t-input) {
    border: 1px solid #E7E7E7;
    border-radius: 8px;
    background: #fff;
    transition: all 0.2s;
    
    &:focus-within {
      border-color: #07C05F;
      box-shadow: 0 0 0 3px rgba(7, 192, 95, 0.1);
    }
    
    &:hover {
      border-color: #07C05F;
    }
    
    .t-input__inner {
      border: none !important;
      box-shadow: none !important;
      outline: none !important;
      background: transparent;
      font-size: 15px;
      font-family: "PingFang SC", sans-serif;
      
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
    margin-bottom: 18px;
    
    &:last-child {
      margin-bottom: 0;
    }
  }
  
  :deep(.t-form-item__control) {
    width: 100%;
  }
}

.submit-button {
  height: 46px;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 500;
  font-family: "PingFang SC", sans-serif;
  margin: 20px 0 16px 0;
  transition: all 0.3s;

  :deep(.t-button) {
    background-color: #07C05F;
    border-color: #07C05F;

    &:hover {
      background-color: #06a855;
      border-color: #06a855;
      transform: translateY(-1px);
      box-shadow: 0 4px 12px rgba(7, 192, 95, 0.3);
    }

    &:active {
      transform: translateY(0);
    }
  }
}

.form-footer {
  text-align: center;
  font-size: 14px;
  color: #6B7280;
  font-family: "PingFang SC", sans-serif;
  margin-top: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #E5E7EB;

  .link-button {
    color: #07C05F;
    text-decoration: none;
    margin-left: 4px;
    font-weight: 500;
    transition: all 0.2s;

    &:hover {
      color: #06a855;
      text-decoration: underline;
    }
  }
}

.login-features {
  margin-top: 20px;
  padding: 0;

  .feature-item {
    display: flex;
    align-items: center;
    margin-bottom: 12px;
    font-size: 13px;
    color: #4B5563;
    font-family: "PingFang SC", sans-serif;

    &:last-child {
      margin-bottom: 0;
    }

    .feature-icon {
      width: 20px;
      height: 20px;
      border-radius: 50%;
      background: #D1FAE5;
      color: #059669;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 12px;
      font-weight: 700;
      margin-right: 10px;
      flex-shrink: 0;
    }

    .feature-text {
      line-height: 1.4;
    }
  }
}

/* Animations */
@keyframes slideInRight {
  from {
    opacity: 0;
    transform: translateX(20px);
  }
  to {
    opacity: 1;
    transform: translateX(0);
  }
}

/* Responsive Design */
@media (max-width: 1024px) {
  .showcase-subtitle {
    font-size: 18px;
  }

  .header-logo {
    top: 26px;
    left: 40px;

    .logo-image {
      width: 100px;
    }
  }

  .header-links {
    top: 22px;
    right: 22px;
    gap: 8px;

    .link-text {
      display: none;
    }

    .header-link {
      padding: 10px;
      gap: 0;
    }
  }
}

@media (max-width: 768px) {
  .login-layout {
    flex-direction: column;
  }

  .showcase-section {
    flex: 0 0 auto;
    min-height: 50vh;
    padding: 40px 24px;
  }

  .showcase-content {
    max-width: 100%;
  }

  .header-logo {
    top: 22px;
    left: 30px;

    .logo-image {
      width: 80px;
    }
  }

  .showcase-subtitle {
    font-size: 16px;
    margin-bottom: 24px;
  }

  .feature-tags {
    margin-bottom: 24px;
  }

  .carousel-container {
    margin-top: 24px;
  }

  .form-section {
    flex: 0 0 auto;
    padding: 24px;
  }

  .header-links {
    top: 18px;
    right: 18px;
    gap: 8px;

    .link-text {
      display: inline;
    }

    .header-link {
      padding: 8px 12px;
      font-size: 12px;
    }
  }

  .form-card {
    padding: 32px 24px;
  }

  .form-title {
    font-size: 22px;
  }
}

@media (max-width: 480px) {
  .showcase-section {
    padding: 32px 20px;
  }

  .header-logo {
    top: 18px;
    left: 20px;

    .logo-image {
      width: 70px;
    }
  }

  .showcase-subtitle {
    font-size: 14px;
  }

  .tag {
    font-size: 12px;
    padding: 6px 16px;
  }

  .form-section {
    padding: 20px;
  }

  .header-links {
    top: 14px;
    right: 14px;
    gap: 6px;
    flex-wrap: wrap;

    .header-link {
      padding: 7px 10px;
      font-size: 11px;
    }
  }

  .form-card {
    padding: 28px 20px;
  }

  .form-header {
    margin-bottom: 24px;
  }
}
</style>
