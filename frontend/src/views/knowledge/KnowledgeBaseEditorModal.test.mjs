import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')

test('editing a knowledge base closes the editor after a successful save', () => {
  assert.match(source, /emit\('success', kbId\)\s*handleClose\(\)/)
})

test('the first successful create stays open for follow-up configuration', () => {
  const createBranch = source.match(
    /if \(editorMode\.value === 'create'\) \{([\s\S]*?)^\s{4}\} else \{/m
  )?.[1]

  assert.ok(createBranch, 'expected to find the create branch')
  assert.doesNotMatch(createBranch, /handleClose\(\)/)
  assert.match(createBranch, /savedKbId\.value = createdKbId/)
})

test('save button labels distinguish create from save-and-close', () => {
  assert.match(
    source,
    /const saveButtonLabel = computed\(\(\) =>\s*editorMode\.value === 'create'\s*\? t\('knowledgeEditor\.buttons\.create'\)\s*: t\('knowledgeEditor\.buttons\.saveAndClose'\)\s*\)/
  )
})

test('shows a post-create hint after the first successful save', () => {
  assert.match(source, /const isPostCreateSession = computed\(\(\) => !!savedKbId\.value\)/)
  assert.match(source, /settings-footer-note/)
  assert.match(source, /knowledgeEditor\.postCreateHint\.followUpDesc/)
})

test('create mode seeds the full default image actions table', () => {
  // Regression: an empty imageActions in initFormData made the attribute panel
  // read `undefined.ocr.on_unobserved` the moment the attribute switch was turned
  // on in the create dialog, crashing the modal.
  const initBlock = source.match(/const initFormData[\s\S]*?imageActions: ([^,]+),/)?.[1]
  assert.ok(initBlock, 'expected to find imageActions in initFormData')
  assert.match(initBlock, /mergeImageActions\(\)/)
})

test('edit mode forwards image_processing_config in the update payload', () => {
  // Regression: the edit branch built data.image_processing_config but never
  // put it into updateConfig, so KB-editor changes to the image classification
  // settings were silently dropped (create worked, later edits did not).
  const editBranch = source.match(
    /(\/\/ 编辑模式：分别更新基本信息[\s\S]*?await updateKnowledgeBase\(kbId, \{)/
  )?.[1]
  assert.ok(editBranch, 'expected to find the edit-mode update block')
  assert.match(editBranch, /if \(data\.image_processing_config\) \{\s*updateConfig\.image_processing_config = data\.image_processing_config\s*\}/)
})

test('video parsing saves its own ASR language and never borrows the audio model', () => {
  // The video section is independent: emptying its model must not resurrect the
  // audio section's model, and its language hint has to round-trip.
  const payload = source.match(/data\.video_config = \{([\s\S]*?)\n  \}/)?.[1]
  assert.ok(payload, 'expected to find the video_config payload block')
  // The language is a select now, but the payload contract did not change: a
  // string, and an empty string still means auto-detect.
  assert.match(payload, /language: normalizeVideoLanguage\(formData\.value\.videoConfig\?\.language\)/)
  assert.doesNotMatch(payload, /language: .*__(auto|AUTO)__/)

  const configured = source.match(
    /const videoAudioModelConfigured = computed\(\(\) =>\s*([\s\S]*?)\)\n/
  )?.[1]
  assert.ok(configured, 'expected to find videoAudioModelConfigured')
  assert.match(configured, /isVideoAudioModelConfigured\(formData\.value\?\.videoConfig\)/)
  assert.doesNotMatch(configured, /asrConfig/)
})

test('video frame descriptions carry their own vlm_config, not the image section', () => {
  // The video page owns its vision model and prompt: saving the editor must
  // send video_config.vlm_config, and the warning state must read the video
  // section instead of multimodalConfig.
  const payload = source.match(/data\.video_config = \{([\s\S]*?)\n  \}/)?.[1]
  assert.ok(payload, 'expected to find the video_config payload block')

  const vlmPayload = payload.match(/vlm_config: \{([\s\S]*?)\n    \}/)?.[1]
  assert.ok(vlmPayload, 'expected to find the nested vlm_config payload')
  assert.match(vlmPayload, /enabled: !!videoVLMModelId/)
  assert.match(vlmPayload, /model_id: videoVLMModelId/)
  assert.match(vlmPayload, /description_language: videoVLM\?\.descriptionLanguage \|\| ''/)
  assert.match(vlmPayload, /custom_instructions: videoVLM\?\.customInstructions \|\| ''/)

  const configured = source.match(
    /const videoFrameModelConfigured = computed\(\(\) =>\s*([\s\S]*?)\)\n/
  )?.[1]
  assert.ok(configured, 'expected to find videoFrameModelConfigured')
  assert.match(configured, /isVideoFrameModelConfigured\(formData\.value\?\.videoConfig\)/)
  assert.doesNotMatch(configured, /multimodalConfig/)
})

test('video frame settings round-trip from the stored video_config.vlm_config', () => {
  // The edit-mode initializer is the block that reads the stored KB.
  const editInit = source.match(
    /modelId: kb\.video_config\?\.model_id \|\| '',[\s\S]*?vlm: \{([\s\S]*?)\n\s*\}\n\s*\},/
  )?.[1]
  assert.ok(editInit, 'expected to find the stored video vlm initializer')
  assert.match(editInit, /modelId: kb\.video_config\?\.vlm_config\?\.model_id \|\| ''/)
  assert.match(editInit, /descriptionLanguage: kb\.video_config\?\.vlm_config\?\.description_language \|\| ''/)
  assert.match(editInit, /customInstructions: kb\.video_config\?\.vlm_config\?\.custom_instructions \|\| ''/)
})

test('video direct read offers a transport selector with a per-choice explanation', () => {
  // 传输方式是直读自己的设置：只在 video_direct 下显示，选项把限制直接写进
  // 下拉项（和解析方式一样），收起后仍能看到当前一档承诺了什么。
  const block = source.match(
    /<!-- 传输方式：([\s\S]*?)<div v-if="formData\.videoConfig\.enabled && videoModeUsesDirect" class="setting-row">([\s\S]*?)\n            <\/div>\n          <\/div>/
  )?.[2]
  assert.ok(block, 'expected to find the transport setting row')
  assert.match(block, /v-model="formData\.videoConfig\.directInput"/)
  assert.match(block, /:options="videoDirectInputOptions"/)
  assert.match(block, /knowledgeEditor\.video\.directInputLabel/)
  assert.match(block, /selectedDirectInputDescription/)
  assert.match(block, /knowledgeEditor\.video\.directInputCostHint/)

  const options = source.match(
    /const videoDirectInputOptions = computed\(\(\) => \[([\s\S]*?)\n\]\)/
  )?.[1]
  assert.ok(options, 'expected to find videoDirectInputOptions')
  for (const value of ['auto', 'base64', 'upload', 'url']) {
    assert.match(options, new RegExp(`value: '${value}'`), `missing the ${value} option`)
  }
  for (const key of [
    'directInputAuto',
    'directInputBase64',
    'directInputUpload',
    'directInputUrl',
  ]) {
    assert.match(options, new RegExp(`knowledgeEditor\\.video\\.${key}`))
    assert.match(options, new RegExp(`knowledgeEditor\\.video\\.${key}Desc`))
  }

  // 当前一档的说明按后端同一套归一读取，老行（空值）显示 auto 的说明。
  const selected = source.match(
    /const selectedDirectInputDescription = computed\(\(\) => \{([\s\S]*?)\n\}\)/
  )?.[1]
  assert.ok(selected, 'expected to find selectedDirectInputDescription')
  assert.match(selected, /normalizeVideoDirectInput\(formData\.value\?\.videoConfig\?\.directInput\)/)
})

test('video transport is sent on save and round-trips from the stored config', () => {
  // 保存必须显式发送 direct_input：缺省在后端等于 auto，但显式发送才能让
  // 「刚刚从 base64 改回 auto」这种修改落库。读取侧对老行归一成 auto。
  const payload = source.match(/data\.video_config = \{([\s\S]*?)\n  \}/)?.[1]
  assert.ok(payload, 'expected to find the video_config payload block')
  assert.match(
    payload,
    /direct_input: normalizeVideoDirectInput\(formData\.value\.videoConfig\?\.directInput\)/
  )

  const editInit = source.match(
    /videoModelId: kb\.video_config\?\.video_model_id \|\| '',([\s\S]*?)directPrompt: kb\.video_config\?\.direct_prompt \|\| ''/
  )?.[1]
  assert.ok(editInit, 'expected to find the stored video initializer')
  assert.match(editInit, /directInput: normalizeVideoDirectInput\(kb\.video_config\?\.direct_input\)/)

  const defaults = source.match(/videoConfig: \{([\s\S]*?)\n    \},/)?.[1]
  assert.ok(defaults, 'expected to find the videoConfig defaults')
  assert.match(defaults, /directInput: 'auto' as string/)
})

test('offers the video transcription language as a dropdown, not a free-text field', () => {
  // The field used to be a text box where the operator had to recall vendor
  // codes. It is now a searchable, creatable select; clearing it still means
  // auto-detect, so there is no sentinel value in the model.
  assert.doesNotMatch(source, /v-model="formData\.videoConfig\.language"/)
  assert.match(source, /<t-select[\s\S]{0,220}v-model="videoLanguageModel"/)
  assert.match(source, /:options="videoLanguageOptions"/)
  assert.match(source, /(?<![-\w])filterable(?![-\w])/)
  assert.match(source, /(?<![-\w])creatable(?![-\w])/)
  assert.match(source, /(?<![-\w])clearable(?![-\w])/)

  // The select model normalises to a string in both directions: an empty
  // selection (or a clear) has to leave `''` in formData, not undefined.
  assert.match(
    source,
    /get: \(\) => normalizeVideoLanguage\(formData\.value\?\.videoConfig\?\.language\)/
  )
  assert.match(
    source,
    /formData\.value\.videoConfig\.language = normalizeVideoLanguage\(selected\)/
  )
})

test('keeps ASR vendor language codes as the video option values', () => {
  const optionBlock = source.match(
    /const videoLanguageOptions = computed\(\(\) => \[([\s\S]*?)\n\]\)/
  )?.[1]
  assert.ok(optionBlock, 'expected to find videoLanguageOptions')
  for (const code of ['zh', 'en', 'ja', 'ko', 'ru']) {
    assert.match(optionBlock, new RegExp(`value: '${code}'`), `missing the ${code} option`)
  }
  // Display names come from i18n; a value like 'Chinese' would be stored in
  // video_config.language and silently rejected by the ASR vendor.
  assert.doesNotMatch(optionBlock, /value: '(Chinese|English|Japanese|Korean|Russian)'/)
  assert.doesNotMatch(optionBlock, /__(auto|AUTO)__/)
})

test('labels the video language options in all six locale bundles', () => {
  for (const locale of ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'ko-KR', 'ru-RU']) {
    const bundle = readFileSync(new URL(`../../i18n/locales/${locale}.ts`, import.meta.url), 'utf8')
    const markerAt = bundle.indexOf('      modeAudioOnlyDesc:')
    assert.ok(markerAt >= 0, `${locale} has no video block`)
    const optionsAt = bundle.indexOf('      languageOptions: {', markerAt)
    const optionsEnd = bundle.indexOf('      },', optionsAt)
    assert.ok(optionsAt > markerAt && optionsEnd > optionsAt, `${locale} has no languageOptions block`)
    const block = bundle.slice(optionsAt, optionsEnd)
    for (const code of ['zh', 'en', 'ja', 'ko', 'ru']) {
      assert.match(block, new RegExp(`^\\s{8}${code}: '`, 'm'), `${locale} is missing ${code}`)
    }
  }
})
