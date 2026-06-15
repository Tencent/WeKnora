<template>
  <div class="markdown-test-page">
    <h1 class="page-title">Markdown Rendering Test</h1>
    <p class="page-desc">
      Dev-only page for visual regression testing of chat answer markdown
      (same typography as botmsg / AgentStreamDisplay / embed).
      Add new test cases or paste arbitrary markdown in the editor below.
    </p>

    <!-- Basic Text Styles (GPT markdown test doc alignment) -->
    <section class="test-section">
      <h2>Basic Text Styles</h2>
      <div class="test-case">
        <div class="test-rendered markdown-content" v-html="basicTextHtml"></div>
      </div>
    </section>

    <!-- LaTeX Formulas -->
    <section class="test-section">
      <h2>LaTeX Formulas</h2>
      <div v-for="(tc, i) in latexCases" :key="'latex-' + i" class="test-case">
        <div class="test-raw"><code>{{ tc.raw }}</code></div>
        <div class="test-rendered markdown-content" v-html="tc.html"></div>
      </div>
    </section>

    <!-- Code Blocks -->
    <section class="test-section">
      <h2>Code Blocks</h2>
      <div class="test-case">
        <div class="test-rendered markdown-content" v-html="codeBlockHtml"></div>
      </div>
    </section>

    <!-- Tables -->
    <section class="test-section">
      <h2>Tables</h2>
      <div class="test-case">
        <div class="test-rendered markdown-content" v-html="tableHtml"></div>
      </div>
    </section>

    <!-- Lists & Blockquotes -->
    <section class="test-section">
      <h2>Lists &amp; Blockquotes</h2>
      <div class="test-case">
        <div class="test-rendered markdown-content" v-html="listsHtml"></div>
      </div>
    </section>

    <!-- Mixed Content (LaTeX + code + text) -->
    <section class="test-section">
      <h2>Mixed Content</h2>
      <div class="test-case">
        <div class="test-rendered markdown-content" v-html="mixedHtml"></div>
      </div>
    </section>

    <!-- Mermaid -->
    <section class="test-section">
      <h2>Mermaid Diagram</h2>
      <div class="test-case">
        <div ref="mermaidContainer" class="test-rendered markdown-content" v-html="mermaidHtml"></div>
      </div>
    </section>

    <!-- Streaming Simulation -->
    <section class="test-section">
      <h2>Streaming Simulation</h2>
      <p class="test-hint">Simulates character-by-character streaming, like during a chat response.</p>
      <div class="stream-controls">
        <button @click="startStream" :disabled="isStreaming" class="btn">Start</button>
        <button @click="resetStream" class="btn">Reset</button>
        <label class="speed-label">
          Speed:
          <input type="range" min="10" max="200" v-model.number="streamSpeed" />
          {{ streamSpeed }}ms
        </label>
      </div>
      <div class="test-case">
        <div ref="streamContainer" class="test-rendered markdown-content" v-html="streamHtml"></div>
        <div v-if="isStreaming" class="loading-typing">
          <span></span><span></span><span></span>
        </div>
      </div>
    </section>

    <!-- Custom Editor -->
    <section class="test-section">
      <h2>Custom Input</h2>
      <p class="test-hint">Paste any markdown here to test rendering.</p>
      <textarea v-model="customInput" class="custom-textarea" rows="8"
        placeholder="Type or paste markdown here..."></textarea>
      <div v-if="customInput.trim()" class="test-case">
        <div ref="customContainer" class="test-rendered markdown-content" v-html="customHtml"></div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick, watch } from 'vue';
import 'katex/dist/katex.min.css';
import { sanitizeMarkdownHTML } from '@/utils/security';
import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from '@/utils/chatMarkdownRenderer';
import {
  ensureMermaidInitialized,
  enhanceMarkdownContainer,
  renderMermaidToSvg,
  createMermaidCodeRenderer,
} from '@/utils/mermaidShared';
import {
  replaceIncompleteMermaidWithPlaceholder,
  prepareStreamingMermaidMarkdown,
  extractFirstMermaidCode,
  injectCachedMermaidSvg,
} from '@/utils/chatMessageShared';

ensureMermaidInitialized();

const mermaidRenderer = createChatMarkdownRenderer({
  codeRenderer: createMermaidCodeRenderer('mermaid-test'),
});

const mermaidContainer = ref<HTMLElement | null>(null);
const streamContainer = ref<HTMLElement | null>(null);
const customContainer = ref<HTMLElement | null>(null);

const render = (raw: string): string => {
  if (!raw) return '';
  return renderChatMarkdown(raw, {
    renderer: mermaidRenderer,
    escapeMarkdown: (markdown) => markdown,
    sanitizeHtml: sanitizeMarkdownHTML,
    prepareMarkdown: (markdown) => replaceIncompleteMermaidWithPlaceholder(markdown),
  });
};

const renderStreamMarkdown = (raw: string): string => {
  if (!raw) return '';
  return renderChatMarkdown(raw, {
    renderer: mermaidRenderer,
    escapeMarkdown: (markdown) => markdown,
    sanitizeHtml: sanitizeMarkdownHTML,
    cachedMermaidSvgHtml: streamMermaidSvgHtml.value,
    prepareMarkdown: prepareStreamingMermaidMarkdown,
    injectCachedMermaidSvg,
  });
};

// --- Test Data ---

const basicTextSample = `这是一段普通文本，包含 **加粗**、*斜体*、***加粗斜体***、~~删除线~~、行内 \`code\`。

也可以包含快捷键样式：<kbd>⌘</kbd> + <kbd>K</kbd>。

这里有一个链接：[GitHub](https://github.com)。

> 一级引用
>
> > 嵌套引用，用于对比 GPT 的引用层级与字重。

- [ ] 未完成任务
- [x] 已完成任务
`;

const latexCases = [
  { raw: 'Inline math: $E = mc^2$ in the middle of text.' },
  { raw: 'Block math:\n$$\\int_0^\\infty e^{-x}\\,dx = 1$$' },
  { raw: 'Chemical formula: $\\mathrm{Mg}^{2+} + 2\\mathrm{OH}^{-} = \\mathrm{Mg(OH)}_{2}\\downarrow$' },
  { raw: 'Chemical block:\n$$\\mathrm{Cu}^{2+} + 2\\mathrm{OH}^{-} \\rightarrow \\mathrm{Cu(OH)_2}\\downarrow$$' },
  { raw: 'Summation: $\\sum_{i=1}^{n} i = \\frac{n(n+1)}{2}$' },
  { raw: 'Matrix:\n$$\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}$$' },
  { raw: 'Escaped delimiters: \\(\\alpha + \\beta = \\gamma\\) and \\[\\int_a^b f(x)\\,dx\\]' },
].map((tc) => ({ ...tc, html: '' }));

const codeBlockSample = `Here is some Python:

\`\`\`python
def fibonacci(n: int) -> int:
    """Calculate the nth Fibonacci number."""
    if n <= 1:
        return n
    return fibonacci(n - 1) + fibonacci(n - 2)

print(fibonacci(10))  # 55
\`\`\`

And inline code: \`const x = 42;\`
`;

const tableSample = `| Element | Symbol | Atomic Number |
|---------|--------|:-------------:|
| Hydrogen | H | 1 |
| Helium | He | 2 |
| Lithium | Li | 3 |
| Carbon | C | 6 |
`;

const listsSample = `### Ordered List
1. First item
2. Second item
   1. Nested item A
   2. Nested item B
3. Third item

### Unordered List
- Alpha
- Beta
  - Sub-item
  - Another sub-item
- Gamma

### Blockquote
> This is a blockquote with **bold** and *italic* text.
>
> It can span multiple paragraphs.
`;

const mixedSample = `## Quadratic Formula

The solutions to $ax^2 + bx + c = 0$ are given by:

$$x = \\frac{-b \\pm \\sqrt{b^2 - 4ac}}{2a}$$

### Example in Python

\`\`\`python
import math

def solve_quadratic(a, b, c):
    discriminant = b**2 - 4*a*c
    if discriminant < 0:
        return None
    x1 = (-b + math.sqrt(discriminant)) / (2*a)
    x2 = (-b - math.sqrt(discriminant)) / (2*a)
    return x1, x2
\`\`\`

| a | b | c | Solutions |
|---|---|---|-----------|
| 1 | -3 | 2 | $x = 1, 2$ |
| 1 | 0 | -4 | $x = \\pm 2$ |
| 1 | 2 | 5 | No real solutions |

> **Note:** The discriminant $\\Delta = b^2 - 4ac$ determines the nature of the roots.
`;

const mermaidSample = `\`\`\`mermaid
graph TD
    A[Start] --> B{Decision}
    B -->|Yes| C[Process A]
    B -->|No| D[Process B]
    C --> E[End]
    D --> E
\`\`\`
`;

// --- Streaming Simulation ---
const fullStreamText = `The energy-mass equivalence is $E = mc^2$.

In chemistry, the neutralization reaction:

$$\\mathrm{Mg}^{2+} + 2\\mathrm{OH}^{-} = \\mathrm{Mg(OH)}_2\\downarrow$$

Here is a code example:

\`\`\`python
def greet(name):
    print(f"Hello, {name}!")
\`\`\`

And the derivative rule: $\\frac{d}{dx}\\sin x = \\cos x$.

\`\`\`mermaid
graph TD
    A[Start] --> B{Decision}
    B -->|Yes| C[Process A]
    B -->|No| D[Process B]
    C --> E[End]
    D --> E
\`\`\`

### Ordered List
1. First item
2. Second item
   1. Nested item A
   2. Nested item B
3. Third item

### Unordered List
- Alpha
- Beta
  - Sub-item
  - Another sub-item
- Gamma

### Blockquote
> This is a blockquote with **bold** and *italic* text.
>
> It can span multiple paragraphs.

Done.`;

const streamBuffer = ref('');
const isStreaming = ref(false);
const streamSpeed = ref(30);
const customInput = ref('');
const streamMermaidSvgHtml = ref('');
let streamTimer: ReturnType<typeof setInterval> | null = null;
let streamMermaidRenderId = 0;
let streamMermaidRenderTask: Promise<void> | null = null;

// Pre-render static fixtures once so streaming ticks do not reset other sections.
latexCases.forEach((tc) => {
  tc.html = render(tc.raw);
});
const codeBlockHtml = render(codeBlockSample);
const basicTextHtml = render(basicTextSample);
const tableHtml = render(tableSample);
const listsHtml = render(listsSample);
const mixedHtml = render(mixedSample);
const mermaidHtml = render(mermaidSample);

const streamHtml = computed(() => renderStreamMarkdown(streamBuffer.value));
const customHtml = computed(() => render(customInput.value));

const cacheStreamMermaidSvg = async () => {
  if (streamMermaidSvgHtml.value) return;

  const code = extractFirstMermaidCode(streamBuffer.value);
  if (!code) return;

  if (!streamMermaidRenderTask) {
    streamMermaidRenderTask = (async () => {
      const svg = await renderMermaidToSvg(code, `mermaid-stream-${++streamMermaidRenderId}`);
      if (svg) streamMermaidSvgHtml.value = svg;
    })().finally(() => {
      streamMermaidRenderTask = null;
    });
  }

  await streamMermaidRenderTask;
};

const startStream = () => {
  resetStream();
  isStreaming.value = true;
  let idx = 0;
  const tick = () => {
    if (idx >= fullStreamText.length) {
      if (streamTimer) clearInterval(streamTimer);
      isStreaming.value = false;
      return;
    }
    streamBuffer.value += fullStreamText[idx++];
  };
  streamTimer = setInterval(tick, streamSpeed.value);
};

const resetStream = () => {
  if (streamTimer) clearInterval(streamTimer);
  streamBuffer.value = '';
  streamMermaidSvgHtml.value = '';
  streamMermaidRenderTask = null;
  isStreaming.value = false;
};

const refreshMermaid = async (root?: HTMLElement | null) => {
  await nextTick();
  await enhanceMarkdownContainer(root ?? mermaidContainer.value);
};

onMounted(() => refreshMermaid());

watch(streamBuffer, () => {
  void cacheStreamMermaidSvg();
});

watch(streamHtml, () => {
  nextTick(() => refreshMermaid(streamContainer.value));
});

watch(isStreaming, (streaming) => {
  if (!streaming) {
    void cacheStreamMermaidSvg().then(() => refreshMermaid(streamContainer.value));
  }
});

let customMermaidTimer: ReturnType<typeof setTimeout> | null = null;
const COMPLETE_MERMAID_RE = /```mermaid[\s\S]*?```/;

watch(customInput, () => {
  if (!COMPLETE_MERMAID_RE.test(customInput.value)) return;
  if (customMermaidTimer) clearTimeout(customMermaidTimer);
  customMermaidTimer = setTimeout(() => {
    customMermaidTimer = null;
    void refreshMermaid(customContainer.value);
  }, 200);
});
</script>

<style lang="less" scoped>
@import '../../components/css/chat-markdown.less';
@import '../../components/css/chat-citations.less';
@import '../../components/css/chat-message-shared.less';

.markdown-test-page {
  max-width: 860px;
  margin: 0 auto;
  padding: 32px 24px;
  font-family: var(--app-font-family);
}

.page-title {
  font-size: 24px;
  font-weight: 700;
  margin-bottom: 4px;
}

.page-desc {
  color: var(--td-text-color-secondary, #666);
  font-size: 14px;
  margin-bottom: 32px;
}

.test-section {
  margin-bottom: 36px;
  border-bottom: 1px solid var(--td-component-stroke, #e5e5e5);
  padding-bottom: 24px;

  h2 {
    font-size: 18px;
    font-weight: 600;
    margin-bottom: 12px;
  }
}

.test-hint {
  font-size: 13px;
  color: var(--td-text-color-secondary, #999);
  margin-bottom: 8px;
}

.test-case {
  margin: 12px 0;
}

.test-raw {
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  padding: 6px 10px;
  border-radius: 4px;
  margin-bottom: 6px;
  font-size: 13px;
  overflow-x: auto;

  code {
    white-space: pre-wrap;
    word-break: break-all;
  }
}

.test-rendered {
  padding: 8px 12px;
  border: 1px solid var(--td-component-stroke, #e5e5e5);
  border-radius: 6px;
  background: var(--td-bg-color-container, #fff);
}

.stream-controls {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.btn {
  padding: 4px 16px;
  border: 1px solid var(--td-component-stroke, #ccc);
  border-radius: 4px;
  background: var(--td-bg-color-container, #fff);
  cursor: pointer;
  font-size: 13px;

  &:hover {
    background: var(--td-bg-color-container-hover, #f0f0f0);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}

.speed-label {
  font-size: 13px;
  display: flex;
  align-items: center;
  gap: 6px;

  input[type="range"] {
    width: 120px;
  }
}

.custom-textarea {
  width: 100%;
  padding: 10px;
  font-family: var(--app-font-family-mono);
  font-size: 13px;
  border: 1px solid var(--td-component-stroke, #ccc);
  border-radius: 6px;
  resize: vertical;
  box-sizing: border-box;
  margin-bottom: 12px;
}

.loading-typing {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px 0;

  span {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--td-brand-color, #0052d9);
    animation: typingBounce 1.4s ease-in-out infinite;

    &:nth-child(1) {
      animation-delay: 0s;
    }

    &:nth-child(2) {
      animation-delay: 0.2s;
    }

    &:nth-child(3) {
      animation-delay: 0.4s;
    }
  }
}

@keyframes typingBounce {

  0%,
  60%,
  100% {
    transform: translateY(0);
  }

  30% {
    transform: translateY(-8px);
  }
}

// Chat answer markdown — shared with botmsg / AgentStreamDisplay / embed
.markdown-content {
  // Dev page intentionally uses the same chat Markdown mixin as runtime chat.
  // Keep visual changes in chat-markdown.less so this page remains a regression target.
  .chat-markdown-typography();
  .chat-citation-pills();
}
</style>
