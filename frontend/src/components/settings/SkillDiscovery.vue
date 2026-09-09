<template>
  <div class="skill-discovery">
    <div class="discovery-toolbar">
      <t-input v-model="search" clearable :placeholder="t('skillDiscovery.search')">
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <t-select v-model="category" :options="categories" class="category-select" />
    </div>
    <div class="discovery-note">
      <t-icon name="info-circle" size="18px" />
      <p>{{ t('skillDiscovery.runtimeHint') }}</p>
    </div>
    <div v-if="error" class="discovery-error" role="alert">
      {{ error }} <t-button theme="default" variant="text" @click="load">{{ t('skillDiscovery.retry') }}</t-button>
    </div>
    <t-loading v-if="loading" />
    <template v-else>
      <section v-for="group in groups" :key="group.kind" class="discovery-section">
        <div class="discovery-section__heading">
          <h3>{{ t(`skillDiscovery.${group.kind === 'builtin' ? 'builtins' : 'recommended'}`) }}</h3>
          <span>{{ group.items.length }}</span>
        </div>
        <p class="discovery-section__description">{{ t(`skillDiscovery.${group.kind === 'builtin' ? 'builtinHint' : 'externalHint'}`) }}</p>
        <div class="discovery-grid">
          <article v-for="item in group.items" :key="item.id" class="discovery-card" :data-tone="tone(item)">
            <div class="discovery-card__top">
              <span class="discovery-icon"><t-icon :name="skillIcon(item)" size="22px" /></span>
              <span class="discovery-kind">{{ t(item.distribution === 'builtin' ? `skillDiscovery.categories.${item.category}` : 'skillDiscovery.linkOnly') }}</span>
            </div>
            <h4>{{ localized(item.title) }}</h4>
            <p class="discovery-card__description">{{ localized(item.description) }}</p>
            <span v-if="item.runtime === 'local_browser'" class="discovery-runtime">{{ t('skillDiscovery.localBrowser') }}</span>
            <div class="discovery-meta">
              <span>{{ item.publisher }}</span><span>·</span>
              <a :href="item.license_url" target="_blank" rel="noopener noreferrer">{{ item.license }}</a>
              <span v-if="item.version" class="discovery-version">{{ item.version }}</span>
            </div>
            <div class="discovery-card__footer">
              <a :href="item.source_url" target="_blank" rel="noopener noreferrer">{{ t('skillDiscovery.source') }} <t-icon name="jump" size="13px" /></a>
              <t-button v-if="item.distribution === 'builtin'" class="discovery-enable" theme="default" variant="outline" size="small"
                @click="emit('install', item)">{{ t('settings.skills.installToSandbox') }}</t-button>
              <a v-else class="discovery-official" :href="item.docs_url || item.source_url" target="_blank" rel="noopener noreferrer">
                {{ t('skillDiscovery.officialGuide') }} <t-icon name="jump" size="13px" />
              </a>
            </div>
          </article>
        </div>
      </section>
      <t-empty v-if="!groups.length" :description="t('skillDiscovery.noResults')" />
    </template>
    <section class="discovery-migration">
      <div><h3>{{ t('skillDiscovery.migrateTitle') }}</h3><p>{{ t('skillDiscovery.migrateHint') }}</p></div>
      <t-button theme="default" variant="outline" @click="showMigration = true">{{ t('skillDiscovery.migrate') }}</t-button>
    </section>
    <SettingDrawer v-model:visible="showMigration" :title="t('skillDiscovery.migrateTitle')" width="620px"
      :confirm-text="t(plan ? 'skillDiscovery.startMigration' : 'skillDiscovery.preview')" :confirm-loading="migrating"
      :confirm-disabled="!sourceId || !targetId || (plan !== null && !plan.some(item => !item.blocker)) || !!migrationResult"
      @confirm="migrationStep" @cancel="showMigration = false">
      <div class="migration-form">
        <p>{{ t('skillDiscovery.migrateDetails') }}</p>
        <t-button theme="default" variant="outline" @click="uiStore.openSettings('sandbox')">{{ t('skillDiscovery.configureSandbox') }}</t-button>
        <label>{{ t('skillDiscovery.sourceSandbox') }}</label>
        <t-select v-model="sourceId" :disabled="migrating" :options="configOptions" />
        <label>{{ t('skillDiscovery.targetSandbox') }}</label>
        <t-select v-model="targetId" :disabled="migrating" :options="configOptions.filter(item => item.value !== sourceId)" />
        <div v-if="migrationError" class="discovery-error" role="alert">{{ migrationError }}</div>
        <div v-if="plan" class="migration-plan">
          <t-empty v-if="!plan.length" :description="t('skillDiscovery.noSkillsToMigrate')" />
          <article v-for="item in plan" :key="item.skill_id">
            <div><strong>{{ item.name }}</strong><span>{{ item.version }}</span></div>
            <code v-if="item.sha256" :title="item.sha256">SHA256 {{ item.sha256.slice(0, 16) }}…</code>
            <p v-if="item.blocker">{{ t(`skillDiscovery.blockers.${item.blocker}`) }}</p>
            <span v-else>{{ t('skillDiscovery.readyToMigrate') }}</span>
          </article>
        </div>
        <div v-if="migrationResult" role="status">
          <p>{{ t('skillDiscovery.migrationStarted', { count: Object.keys(migrationResult.installs).length }) }}</p>
          <p v-for="(reason, name) in migrationResult.errors" :key="name">{{ name }}: {{ reason }}</p>
        </div>
      </div>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from './SettingDrawer.vue'
import { useUIStore } from '@/stores/ui'
import { listSkillDiscovery, previewSkillMigration, migrateSkills, type DiscoverySkill, type SkillCatalogItem, type SkillMigrationItem } from '@/api/skill'
import type { SandboxConfigRecord } from '@/api/system'

const props = defineProps<{ catalog: SkillCatalogItem[]; configs: SandboxConfigRecord[] }>()
const emit = defineEmits<{ install: [item: DiscoverySkill]; migrated: [] }>()
const { t, locale } = useI18n()
const uiStore = useUIStore()
const items = ref<DiscoverySkill[]>([])
const loading = ref(true)
const error = ref('')
const search = ref('')
const category = ref('all')
const tone = (item: DiscoverySkill) => {
  if (item.category === 'browser') return 'cyan'
  if (item.category === 'analysis') return 'violet'
  if (item.name.includes('xlsx')) return 'green'
  if (item.name.includes('pdf')) return 'rose'
  if (item.name.includes('ppt') || item.name === 'powerpoint') return 'amber'
  return 'blue'
}
const skillIcon = (item: DiscoverySkill) => {
  if (item.category === 'browser') return 'internet'
  if (item.category === 'analysis') return 'chart-bar'
  if (item.name.includes('xlsx')) return 'table'
  if (item.name.includes('ppt') || item.name === 'powerpoint') return 'slideshow'
  return 'file'
}
const localized = (value: Record<string, string>) => value[locale.value] || value['en-US'] || ''
const categories = computed(() => ['all', 'office', 'analysis', 'browser'].map(value => ({ value, label: t(`skillDiscovery.categories.${value}`) })))
const groups = computed(() => ['builtin', 'external_link'].map(kind => ({ kind, items: items.value.filter(item => {
  const query = search.value.trim().toLowerCase()
  return item.distribution === kind && (category.value === 'all' || category.value === item.category)
    && `${localized(item.title)} ${localized(item.description)} ${item.publisher} ${item.name}`.toLowerCase().includes(query)
}) })).filter(group => group.items.length))
async function load() {
  loading.value = true; error.value = ''
  try { items.value = (await listSkillDiscovery()).data || [] }
  catch (err: any) { error.value = err?.message || t('skillDiscovery.loadFailed') }
  finally { loading.value = false }
}
const showMigration = ref(false)
const sourceId = ref('')
const targetId = ref('')
const migrating = ref(false)
const migrationError = ref('')
const plan = ref<SkillMigrationItem[] | null>(null)
const migrationResult = ref<{ installs: Record<string, string>; errors: Record<string, string> } | null>(null)
const configOptions = computed(() => props.configs.filter(cfg => cfg.sandbox_type !== 'disabled').map(cfg => ({ label: cfg.name, value: cfg.id })))
watch([sourceId, targetId], () => { plan.value = null; migrationResult.value = null; migrationError.value = '' })
async function migrationStep() {
  migrating.value = true; migrationError.value = ''
  const source = sourceId.value, target = targetId.value
  try {
    if (!plan.value) {
      const result = await previewSkillMigration(source, target)
      if (source === sourceId.value && target === targetId.value) plan.value = result.data
    } else {
      migrationResult.value = (await migrateSkills(source, target, plan.value)).data
      emit('migrated')
    }
  } catch (err: any) { migrationError.value = err?.message || t('skillDiscovery.migrationFailed') }
  finally { migrating.value = false }
}
onMounted(load)
</script>

<style scoped lang="less">
.skill-discovery { color: var(--td-text-color-primary); }
.discovery-card {
  --skill-accent: var(--discovery-blue, #4875ce);
  &[data-tone='green'] { --skill-accent: var(--discovery-green, #25876b); }
  &[data-tone='amber'] { --skill-accent: var(--discovery-amber, #bb7627); }
  &[data-tone='rose'] { --skill-accent: var(--discovery-rose, #c55b70); }
  &[data-tone='violet'] { --skill-accent: var(--discovery-violet, #8260c8); }
  &[data-tone='cyan'] { --skill-accent: var(--discovery-cyan, #25899d); }
}
.discovery-toolbar { display: flex; gap: 12px; margin: 24px 0 16px; .category-select { width: 160px; flex-shrink: 0; } }
.discovery-note { display: flex; align-items: flex-start; gap: 10px; padding: 14px 16px; background: color-mix(in srgb, #4875ce 6%, var(--td-bg-color-container)); border: 1px solid color-mix(in srgb, #4875ce 13%, var(--td-component-stroke)); border-radius: 8px; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.7; p { margin: 0; } .t-icon { margin-top: 3px; flex-shrink: 0; color: #668acb; } }
.discovery-section { margin-top: 28px; }
.discovery-section__heading { display: flex; align-items: center; gap: 9px; h3 { margin: 0; font-size: 15px; font-weight: 600; } span { color: var(--td-text-color-placeholder); font-size: 12px; } }
.discovery-section__description { font-size: 13px; line-height: 1.7; color: var(--td-text-color-secondary); margin: 7px 0 16px; }
.discovery-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(230px, 1fr)); gap: 14px; }
.discovery-card { border: 1px solid var(--td-component-stroke); border-top: 2px solid color-mix(in srgb, var(--skill-accent) 55%, var(--td-component-stroke)); border-radius: 12px; padding: 18px; display: flex; flex-direction: column; background: linear-gradient(145deg, color-mix(in srgb, var(--skill-accent) 5%, transparent), transparent 45%), var(--td-bg-color-container); transition: border-color .15s, box-shadow .15s; &:hover { border-color: color-mix(in srgb, var(--skill-accent) 55%, var(--td-component-stroke)); box-shadow: 0 4px 16px color-mix(in srgb, var(--skill-accent) 9%, transparent); } h4 { font-size: 15px; font-weight: 600; margin: 16px 0 8px; } a { color: var(--td-text-color-secondary); text-decoration: none; &:hover { color: var(--td-text-color-primary); text-decoration: underline; } } }
.discovery-card__top { display: flex; align-items: center; justify-content: space-between; }
.discovery-icon { display: grid; place-items: center; width: 42px; height: 42px; border-radius: 11px; background: color-mix(in srgb, var(--skill-accent) 12%, transparent); color: var(--skill-accent); }
.discovery-runtime { font-size: 11px; color: var(--td-text-color-secondary); margin-bottom: 10px; }
.discovery-kind { color: var(--skill-accent); font-size: 11px; background: color-mix(in srgb, var(--skill-accent) 8%, transparent); border-radius: 5px; padding: 3px 7px; }
.discovery-version { margin-left: auto; font-variant-numeric: tabular-nums; }
.discovery-enable { color: var(--skill-accent); border-color: color-mix(in srgb, var(--skill-accent) 30%, var(--td-component-stroke)); background: color-mix(in srgb, var(--skill-accent) 5%, var(--td-bg-color-container)); &:hover { background: color-mix(in srgb, var(--skill-accent) 12%, var(--td-bg-color-container)); } }
:global([theme-mode='dark']) {
  --discovery-blue: #89a9e8;
  --discovery-green: #6ab89d;
  --discovery-amber: #d2a364;
  --discovery-rose: #dc8b9e;
  --discovery-violet: #b098e4;
  --discovery-cyan: #6bb6c6;
}
.discovery-card__description { margin: 0 0 16px; font-size: 13px; line-height: 1.7; color: var(--td-text-color-secondary); flex: 1; }
.discovery-meta { display: flex; gap: 6px; align-items: center; font-size: 11px; color: var(--td-text-color-placeholder); flex-wrap: wrap; }
.discovery-card__footer { border-top: 1px solid var(--td-component-stroke); margin-top: 14px; padding-top: 13px; display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 12px; }
.discovery-migration { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin-top: 30px; padding: 20px 0; border-top: 1px solid var(--td-component-stroke); h3 { font-size: 14px; margin: 0 0 8px; } p { font-size: 13px; line-height: 1.7; color: var(--td-text-color-secondary); margin: 0; } .t-button { flex-shrink: 0; } }
.discovery-error { margin: 14px 0; color: var(--td-error-color); font-size: 13px; }
.migration-form { display: flex; flex-direction: column; gap: 14px; p { font-size: 13px; color: var(--td-text-color-secondary); line-height: 1.8; margin: 0; } label { margin-top: 8px; font-size: 13px; } .t-button { align-self: flex-start; } }
.migration-plan article { padding: 14px 0; border-bottom: 1px solid var(--td-component-stroke); font-size: 12px; div { display: flex; gap: 12px; margin-bottom: 6px; } span, code { color: var(--td-text-color-secondary); } code { display: block; margin-bottom: 6px; } }
@media (max-width: 600px) { .discovery-grid { grid-template-columns: 1fr; } .discovery-toolbar { flex-direction: column; .category-select { width: 100%; } } .discovery-migration { flex-direction: column; align-items: flex-start; } }
</style>
