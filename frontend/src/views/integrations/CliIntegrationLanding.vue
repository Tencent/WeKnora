<template>
  <IntegrationLandingLayout
    :title="$t('integrations.cli.title')"
    :subtitle="$t('integrations.cli.subtitle')"
  >
    <template #actions>
      <IntegrationExternalCta
        :label="$t('integrations.cli.docs')"
        :hint="$t('integrations.cli.docsHint')"
        @click="openDocs"
      >
        <template #icon><t-icon name="code" /></template>
      </IntegrationExternalCta>
    </template>

    <template #main>
      <div class="landing-group">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('integrations.cli.quickstart') }}</h4>
          <ol class="landing-steps">
            <li v-for="(step, index) in steps" :key="step.key" class="landing-step">
              <span class="landing-step-num">{{ index + 1 }}</span>
              <div class="landing-step-body">
                <div class="landing-step-title">{{ $t(`integrations.cli.${step.key}Title`) }}</div>
                <p class="landing-step-desc">{{ $t(`integrations.cli.${step.key}Desc`) }}</p>
                <div class="landing-step-embed code-toolbar">
                  <pre class="code-toolbar__code">{{ step.command }}</pre>
                  <t-button
                    class="code-toolbar__copy"
                    size="small"
                    variant="text"
                    shape="square"
                    :title="$t('integrations.cli.copy')"
                    :aria-label="$t('integrations.cli.copy')"
                    @click="copy(step.command)"
                  >
                    <t-icon name="file-copy" size="16px" />
                  </t-button>
                </div>
              </div>
            </li>
          </ol>
        </section>
      </div>
    </template>

    <template #aside>
      <div class="landing-group">
        <section v-for="example in examples" :key="example.key" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t(`integrations.cli.${example.key}Title`) }}</h4>
          <p class="field-desc">{{ $t(`integrations.cli.${example.key}Desc`) }}</p>
          <div class="code-toolbar">
            <pre class="code-toolbar__code">{{ example.command }}</pre>
            <t-button
              class="code-toolbar__copy"
              size="small"
              variant="text"
              shape="square"
              :title="$t('integrations.cli.copy')"
              :aria-label="$t('integrations.cli.copy')"
              @click="copy(example.command)"
            >
              <t-icon name="file-copy" size="16px" />
            </t-button>
          </div>
        </section>
      </div>
    </template>
  </IntegrationLandingLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useApiBaseUrlDisplay } from '@/composables/useApiBaseUrlDisplay'
import { copyWithToast } from '@/utils/clipboard'
import IntegrationLandingLayout from './IntegrationLandingLayout.vue'
import IntegrationExternalCta from './IntegrationExternalCta.vue'
import { buildCLIConnectCommand } from './cliIntegration'

const { apiBaseUrlDisplay } = useApiBaseUrlDisplay()
const steps = computed(() => [
  {
    key: 'install',
    command: 'git clone https://github.com/Tencent/WeKnora.git\ncd WeKnora/cli\ngo build -o weknora .\nexport PATH="$PWD:$PATH"',
  },
  { key: 'connect', command: buildCLIConnectCommand(apiBaseUrlDisplay.value, window.location.origin) },
  { key: 'verify', command: 'weknora doctor\nweknora kb list' },
])
const examples = [
  {
    key: 'commands',
    command: 'weknora doc upload ./document.pdf --kb "KB_ID"\nweknora search chunks "query" --kb "KB_ID"\nweknora chat "question" --kb "KB_ID" --format text\nweknora agent list',
  },
  {
    key: 'mcp',
    command: JSON.stringify({
      mcpServers: {
        weknora: { command: 'weknora', args: ['--profile', 'weknora', 'mcp', 'serve'] },
      },
    }, null, 2),
  },
]

const copy = (command: string) => copyWithToast(command, 'integrations.cli.copied')
const openDocs = () => {
  window.open('https://github.com/Tencent/WeKnora/blob/main/cli/README.md', '_blank', 'noopener,noreferrer')
}
</script>
