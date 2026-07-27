# 单图复刻创作 - 后端设计文档

> 基于现有「单图创作」模块（设计文档：`AI电商-单图创作-后端设计文档.md`），新增"单图复刻创作"类型。

---

## 一、需求概述

### 1.1 背景

前端单图创作页面需要分裂为两个独立页面：
- **单图创作**（`single`）：现有功能，不变
- **单图复刻创作**（`replicate`）：新增类型，核心差异在 prompt 拼接方式

### 1.2 三个改动点

| # | 改动 | 说明 |
|---|------|------|
| 1 | 新增单图复刻创作类型 | 内置提示词一分为二（前段 + 后段），用户文案插入中间。内置提示词后台可通过字典管理调整 |
| 2 | 创作记录跳转路由 | 返回任务上下文接口返回 `creationType`，前端据此跳转到对应页面（single / replicate / 未来的 set） |
| 3 | 复刻类型回显隔离 | 返回任务上下文时，只回显用户自己输入的文案（prompt / polishedPrompt），不向前端暴露内置提示词；数据库中的 `final_prompt` 与服务日志仍保留完整内容，便于研发排查 |

### 1.3 复用 vs 新建

| 能力 | 复用现有 | 新建/修改 |
|------|---------|----------|
| 任务表 `ecom_single_image_task` | 共表 | 新增 `creation_type` 字段 |
| 异步生图 `EcomImageGenerateTask` | 完全复用，不改动 | - |
| 积分扣减/退款 | 完全复用 | - |
| 补偿调度 `EcomSingleImageTaskScheduler` | 完全复用 | - |
| 状态查询 / 执行记录 / 历史图片 | 完全复用 | - |
| prompt 拼接 `EcomPromptBuilder` | - | 修改：新增复刻模式拼接逻辑 |
| 提交任务 `submitGenerateTask` | 大部分复用 | 小改：传入 creationType，调用对应 prompt 构建逻辑 |
| 创作记录上下文 `getTaskContext` | 大部分复用 | 小改：`creationType` 从任务记录读取 |
| 内置提示词管理 | `dict_type` + `dict_data` 字典表 | 新增字典类型与字典数据 |

---

## 二、数据库变更

### 2.1 ecom_single_image_task 表新增字段

```sql
ALTER TABLE `ecom_single_image_task`
    ADD COLUMN `creation_type` VARCHAR(20) NOT NULL DEFAULT 'single'
        COMMENT '创作类型：single-单图创作 replicate-单图复刻创作'
        AFTER `task_no`;
```

**说明**：
- 现有数据默认值 `single`，完全兼容
- 不加索引。创作记录列表按 `host_uuid`/`user_uuid` + `status` + `create_time` 查询，`creation_type` 只是 WHERE 附加条件，现有联合索引已足够覆盖
- 未来套图创作可扩展为 `set`

### 2.2 dict_type / dict_data 字典新增

为了让运营可以在系统字典管理后台直接看到并维护该类型，需要同时新增 `dict_type` 和对应的 `dict_data` 记录。

新增 `dict_type = 'ecom_replicate_prompt'`，两条 `dict_data` 记录分别存储内置提示词的前段和后段：

```sql
INSERT INTO `dict_type` (`dict_name`, `dict_type`, `status`, `create_by`, `create_time`, `remark`)
VALUES
('单图复刻创作提示词', 'ecom_replicate_prompt', '0', 'system', NOW(), '电商单图复刻创作-内置提示词');

INSERT INTO `dict_data` (`dict_sort`, `dict_label`, `dict_value`, `dict_type`, `css_class`, `is_default`, `status`, `create_by`, `create_time`, `remark`)
VALUES
(1, '复刻提示词-前段', 'prefix', 'ecom_replicate_prompt', '这里填写内置提示词的前半段内容', 'N', '0', 'system', NOW(), '单图复刻创作-内置提示词前段，运营可在字典管理中编辑 css_class 字段修改'),
(2, '复刻提示词-后段', 'suffix', 'ecom_replicate_prompt', '这里填写内置提示词的后半段内容', 'N', '0', 'system', NOW(), '单图复刻创作-内置提示词后段，运营可在字典管理中编辑 css_class 字段修改');
```

| 字段 | 前段记录 | 后段记录 | 说明 |
|------|---------|---------|------|
| `dict_sort` | 1 | 2 | 排序，前段在前 |
| `dict_label` | 复刻提示词-前段 | 复刻提示词-后段 | 管理后台展示标签 |
| `dict_value` | `prefix` | `suffix` | 程序读取的 key |
| `dict_type` | `ecom_replicate_prompt` | `ecom_replicate_prompt` | 字典类型 |
| `css_class` | 内置前半段文本 | 内置后半段文本 | **实际提示词内容**，运营在字典管理后台编辑此字段 |

**运营编辑方式**：进入系统字典管理页面 → 找到类型 `ecom_replicate_prompt` → 编辑对应记录的 `css_class` 字段即可。

**补充说明**：
- 当前仓库已将 `dict_data.css_class` 扩展为 `TEXT`，可用于存储较长提示词
- 程序读取时仍使用 `dict_type + dict_value` 精确定位前段/后段配置

---

## 三、prompt 拼接逻辑

### 3.1 单图创作（不变）

```
final_prompt = polishedPrompt ?? prompt
```

即：有润色用润色，否则用原始描述。与现有 `EcomPromptBuilder.build()` 逻辑一致。

### 3.2 单图复刻创作（新增）

```
final_prompt = dict[prefix] + "\n" + (polishedPrompt ?? prompt) + "\n" + dict[suffix]
```

即：内置前段 + 用户文案 + 内置后段。

### 3.3 EcomPromptBuilder 改造

现有 `build(prompt, polishedPrompt)` 方法保持不变（单图创作继续调用），新增 `buildReplicate(prompt, polishedPrompt)` 方法：

```java
@Component
@RequiredArgsConstructor
public class EcomPromptBuilder {

    private final DictDataService dictDataService;

    /**
     * 单图创作：优先润色，否则原始。
     */
    public String build(String prompt, String polishedPrompt) {
        if (StringUtils.hasText(polishedPrompt)) {
            return polishedPrompt.trim();
        }
        return prompt == null ? "" : prompt.trim();
    }

    /**
     * 单图复刻创作：内置前段 + 用户文案 + 内置后段。
     */
    public String buildReplicate(String prompt, String polishedPrompt) {
        String userInput = build(prompt, polishedPrompt);
        String prefix = loadReplicatePrompt("prefix");
        String suffix = loadReplicatePrompt("suffix");
        return joinPromptParts(prefix, userInput, suffix);
    }

    private String loadReplicatePrompt(String dictValue) {
        DictData dict = dictDataService.getDictByDictTypeAndDictValue(
                "ecom_replicate_prompt", dictValue);
        if (dict == null || !StringUtils.hasText(dict.getCssClass())) {
            return "";
        }
        return dict.getCssClass().trim();
    }

    private String joinPromptParts(String prefix, String userInput, String suffix) {
        StringBuilder sb = new StringBuilder();
        if (StringUtils.hasText(prefix)) {
            sb.append(prefix);
        }
        if (StringUtils.hasText(userInput)) {
            if (!sb.isEmpty()) sb.append("\n");
            sb.append(userInput);
        }
        if (StringUtils.hasText(suffix)) {
            if (!sb.isEmpty()) sb.append("\n");
            sb.append(suffix);
        }
        return sb.toString();
    }
}
```

**说明**：
- `loadReplicatePrompt` 每次从 DB 读取，不做内存缓存。因为运营可能随时修改内置提示词，需要实时生效。调用频率低（仅提交任务时触发），无性能问题。
- 如果前后段字典数据缺失或 `css_class` 为空，对应部分跳过拼接，不报错。

---

## 四、核心流程变更

### 4.1 提交任务流程（EcomSingleImageServiceImpl.submitGenerateTask）

变更点用 **[变更]** 标记：

```
Controller: EcomSingleImageController.generate()
    │
    ▼
Service: EcomSingleImageServiceImpl.submitGenerateTask()
    │
    ├── 1. 参数校验（不变）
    │
    ├── 2. 生成 taskNo（不变）
    │
    │  ┌─── @Transactional 事务边界 ──────────────────────────┐
    │  │                                                      │
    ├── 3. 积分扣减 + 记录收支明细（不变）                      │
    │                                                          │
    ├── 4. [变更] 构建 final_prompt                            │
    │     ├── creationType == "single"                         │
    │     │   └── EcomPromptBuilder.build(prompt, polished)    │
    │     └── creationType == "replicate"                      │
    │         └── EcomPromptBuilder.buildReplicate(prompt, polished)
    │                                                          │
    ├── 5. [变更] 任务入库                                     │
    │     ├── task.setCreationType(request.getCreationType())  │
    │     └── 其余字段不变                                     │
    │  │                                                      │
    │  └──────────────────────────────────────────────────────┘
    │
    ├── 6-8. 异步触发、调度兜底、返回结果（不变）
```

### 4.2 异步生图任务（EcomImageGenerateTask）

**不需要核心业务逻辑改动**。异步任务只读 `task.getFinalPrompt()`，不关心 `creationType`。prompt 差异已在提交阶段由 `EcomPromptBuilder` 处理完毕。

**日志策略说明**：
- 保留现有 `finalPrompt` 日志打印行为，复刻模式下完整拼接后的提示词允许出现在服务日志中，便于开发排查问题
- 本次“隔离”仅针对**前端回显接口**，不要求隐藏数据库中的 `final_prompt`，也不要求删除服务日志中的完整 prompt

### 4.3 AI 润色（polish 接口）

**不需要改动**。润色只处理用户输入的文本，与内置提示词无关。复刻类型的润色流程和单图创作完全一致。

### 4.4 任务状态查询 / 执行记录 / 历史图片

**不需要改动**。这些接口查询的维度是 user/host + status + 时间，`creation_type` 不影响查询逻辑。

### 4.5 创作记录列表（EcomCreationRecordServiceImpl.list）

变更点：

1. **查询条件**：`EcomCreationRecordQuery.creationType` 现在支持 `single` / `replicate` / `all`（默认 `all` 查全部）
2. **creationType 映射**：`toCreationRecord()` 中 `creationType` / `creationTypeText` 从任务记录的 `creation_type` 字段读取，不再硬编码为 `"single"`

```java
// 之前：
recordVO.setCreationType("single");
recordVO.setCreationTypeText("单图创作");

// 之后：
recordVO.setCreationType(task.getCreationType());
recordVO.setCreationTypeText(
    "replicate".equals(task.getCreationType()) ? "单图复刻创作" : "单图创作"
);
```

**查询过滤**：
```java
// creationType 过滤逻辑
if (StringUtils.hasText(query.getCreationType())
        && !"all".equalsIgnoreCase(query.getCreationType().trim())) {
    wrapper.eq(EcomSingleImageTask::getCreationType, query.getCreationType().trim());
}
```

### 4.6 返回任务上下文（EcomCreationRecordServiceImpl.getTaskContext）

变更点：

1. **creationType** 从 `task.getCreationType()` 读取，前端据此决定跳转到哪个页面
2. **回显字段不变**：`prompt`（用户原始输入）、`polishedPrompt`（AI 润色结果）正常返回。这两个字段存的就是用户自己的文案，不包含内置提示词
3. **不返回 `finalPrompt`**：上下文接口本来就不返回 `finalPrompt`，所以内置提示词天然对前端不可见

```java
// 之前：
context.setCreationType("single");

// 之后：
context.setCreationType(task.getCreationType());
```

**回显隔离保证**：
- 入库时：`prompt` = 用户原始输入，`polished_prompt` = AI 润色结果，`final_prompt` = 完整拼接版（含内置提示词）
- 回显时：context 接口只返回 `prompt` 和 `polishedPrompt`，不返回 `finalPrompt`
- 日志与数据库仍可保留完整 `final_prompt` 供研发排查使用
- 因此前端回显场景下，复刻类型的内置提示词**天然不会暴露给前端**，无需额外处理

---

## 五、DTO / Entity / VO 变更

### 5.1 EcomGenerateRequest（DTO）

新增 `creationType` 字段：

```java
@Schema(description = "创作类型：single-单图创作 replicate-单图复刻创作",
        requiredMode = Schema.RequiredMode.REQUIRED)
@NotBlank(message = "创作类型不能为空")
@Pattern(regexp = "^(single|replicate)$", message = "创作类型只支持 single/replicate")
private String creationType = "single";
```

### 5.2 EcomSingleImageTask（Entity）

新增 `creationType` 字段：

```java
@TableField("creation_type")
@Schema(description = "创作类型")
private String creationType;
```

### 5.3 EcomCreationRecordQuery（DTO）

`creationType` 字段扩展可选值：

```java
@Schema(description = "创作类型筛选：all/single/replicate", example = "all")
private String creationType = "all";
```

### 5.4 EcomTaskContextVO（VO）

**不需要改动**。`creationType` 字段已存在，只是赋值逻辑从硬编码 `"single"` 改为从任务记录读取。

### 5.5 EcomCreationRecordVO（VO）

**不需要改动**。`creationType` / `creationTypeText` 字段已存在，只是赋值逻辑变更。

---

## 六、接口变更汇总

| 接口 | 路径 | 变更内容 |
|------|------|---------|
| 提交生成任务 | `POST /api/ecommerce/single-image/generate` | 请求体新增 `creationType` 字段 |
| AI 润色 | `POST /api/ecommerce/single-image/polish` | 不变 |
| 查询任务状态 | `GET /api/ecommerce/single-image/task/{taskId}/status` | 不变 |
| 执行记录列表 | `GET /api/ecommerce/single-image/history` | 不变 |
| 历史图片列表 | `GET /api/ecommerce/single-image/history-images` | 不变 |
| 图片下载 | `GET /api/ecommerce/single-image/download` | 不变 |
| 创作记录列表 | `GET /api/ecommerce/creation-record/list` | `creationType` 参数支持 `all`/`single`/`replicate`；返回的 `creationType`/`creationTypeText` 从任务记录读取 |
| 返回任务上下文 | `GET /api/ecommerce/creation-record/{taskId}/context` | `creationType` 从任务记录读取 |

---

## 七、代码改动清单

### 7.1 修改文件

| 文件 | 改动内容 |
|------|---------|
| `EcomGenerateRequest.java` | 新增 `creationType` 字段 + 校验注解 |
| `EcomSingleImageTask.java` | 新增 `creationType` 字段 |
| `EcomPromptBuilder.java` | 注入 `DictDataService`；新增 `buildReplicate()`、`loadReplicatePrompt()`、`joinPromptParts()` 方法 |
| `EcomSingleImageServiceImpl.java` | `submitGenerateTask()` 中根据 `creationType` 调用不同的 prompt 构建方法；任务入库时写入 `creationType` |
| `EcomCreationRecordServiceImpl.java` | `list()` 中 `creationType` 查询过滤逻辑调整；`toCreationRecord()` 中 `creationType`/`creationTypeText` 从任务记录读取；`getTaskContext()` 中 `creationType` 从任务记录读取 |
| `EcomCreationRecordQuery.java` | `creationType` 默认值改为 `"all"`，支持 `all`/`single`/`replicate` |
| `EcomPromptBuilderTest.java` | 补充 `buildReplicate()`、字典缺失、空白拼接等单元测试，并调整构造方式 |
| `EcomSingleImageServiceImplTest.java` | 补充 `single` / `replicate` 分支下 prompt 构建与 `creationType` 入库断言 |
| `EcomCreationRecordServiceImplTest.java` | 调整 `creationType` 默认值相关断言，补充 `all` / `replicate` 查询与上下文回填测试 |

### 7.2 新增文件

无。

### 7.3 不需要改动的文件

| 文件 | 原因 |
|------|------|
| `EcomImageGenerateTask.java` | 继续只读 `finalPrompt`，不关心 `creationType`；现有完整 prompt 日志打印策略保留 |
| `EcomSingleImageTaskScheduler.java` | 补偿扫描按 status 查询，不涉及 `creationType` |
| `EcomSingleImageTaskMapper.xml` | 所有条件更新 SQL 按 `id` + `status` 操作，不涉及 `creationType` |
| `EcomSingleImageController.java` | 透传 request，不需要改动 |
| `EcomCreationRecordController.java` | 透传 query/taskId，不需要改动 |
| `GeminiRequestHelper.java` | 纯工具类，不涉及业务逻辑 |
| 所有 VO 类 | 字段已覆盖，只是赋值逻辑变更 |

---

## 八、数据库迁移脚本

```sql
-- V20260411.001__add_ecom_creation_type.sql

-- 1. ecom_single_image_task 新增 creation_type 字段
ALTER TABLE `ecom_single_image_task`
    ADD COLUMN `creation_type` VARCHAR(20) NOT NULL DEFAULT 'single'
        COMMENT '创作类型：single-单图创作 replicate-单图复刻创作'
        AFTER `task_no`;

-- 2. 新增字典类型 ecom_replicate_prompt
INSERT INTO `dict_type` (`dict_name`, `dict_type`, `status`, `create_by`, `create_time`, `remark`)
VALUES
('单图复刻创作提示词', 'ecom_replicate_prompt', '0', 'system', NOW(), '电商单图复刻创作-内置提示词');

-- 3. 新增字典数据（内置提示词前后段）
INSERT INTO `dict_data` (`dict_sort`, `dict_label`, `dict_value`, `dict_type`, `css_class`, `is_default`, `status`, `create_by`, `create_time`, `remark`)
VALUES
(1, '复刻提示词-前段', 'prefix', 'ecom_replicate_prompt', '请在此填写内置提示词前段', 'N', '0', 'system', NOW(), '单图复刻创作-内置提示词前段'),
(2, '复刻提示词-后段', 'suffix', 'ecom_replicate_prompt', '请在此填写内置提示词后段', 'N', '0', 'system', NOW(), '单图复刻创作-内置提示词后段');
```

---

## 九、前端对接要点

### 9.1 提交任务

请求体新增 `creationType` 字段：
- 单图创作页面传 `"single"`
- 单图复刻创作页面传 `"replicate"`

### 9.2 创作记录跳转

`GET /creation-record/{taskId}/context` 返回的 `creationType` 可能值：

| creationType | 跳转目标 |
|-------------|---------|
| `single` | 单图创作页面 |
| `replicate` | 单图复刻创作页面 |
| `set`（未来） | 套图创作页面 |

### 9.3 复刻页面回显

返回任务上下文中的 `prompt` / `polishedPrompt` 就是用户自己输入的内容，直接回显即可。内置提示词不在返回数据中，前端无需做任何过滤。

补充说明：研发排查时，后端日志和数据库中的 `final_prompt` 仍可看到完整拼接结果，这不属于前端回显范围。

### 9.4 创作记录列表筛选

`creationType` 参数：
- `all`（默认）：查全部类型
- `single`：只查单图创作
- `replicate`：只查单图复刻创作

---

## 十、开发顺序

| 步骤 | 内容 | 预估复杂度 |
|------|------|-----------|
| 1 | 执行数据库迁移（ALTER TABLE + INSERT dict_type/dict_data） | 低 |
| 2 | Entity 新增 `creationType` 字段 | 低 |
| 3 | DTO 新增/修改 `creationType` 字段 | 低 |
| 4 | `EcomPromptBuilder` 新增 `buildReplicate()` | 低 |
| 5 | `EcomSingleImageServiceImpl.submitGenerateTask()` 适配 | 低 |
| 6 | `EcomCreationRecordServiceImpl` 适配（list + getTaskContext） | 低 |
| 7 | 单元测试补充（`EcomPromptBuilderTest` / `EcomSingleImageServiceImplTest` / `EcomCreationRecordServiceImplTest`） | 低 |
| 8 | 联调：运营在字典管理后台填写真实内置提示词 | 低 |

整体改动量小，主要是 prompt 构建逻辑分支 + 若干字段赋值调整 + 少量测试适配，不涉及新表、新接口或流程重构。
