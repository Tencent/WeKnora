# AI电商-单图复刻创作-顶部商品分类与分类提示词联动-后端设计文档

> 文档版本：1.3
> 创建时间：2026-05-12
> 更新时间：2026-05-18
> 对应需求文档：《AI电商-单图复刻创作-顶部商品分类与分类提示词联动-需求文档》

---

## 一、改动概述

### 1.1 一句话总结

单图复刻创作的分类提示词命中依据，从“通过已选商品反查商品分类”改为“以前端页面顶部最终选择的商品分类为准”。

### 1.2 改动范围

| 维度 | 范围 |
|---|---|
| 创作类型 | 仅 `replicate`（单图复刻创作） |
| 不涉及 | `single`（单图自由创作）、`set`（套图创作） |
| 提交接口 | 复用现有 `POST /api/ecommerce/single-image/generate` |
| 页面初始化接口 | 新增 `GET /api/ecommerce/single-image/replicate/init-config` |
| 返回任务接口 | 复用现有 `GET /api/ecommerce/creation-record/{taskId}/context` |
| 商品选择弹窗 | 复用现有商品分页查询接口，补齐分类过滤的使用方式 |
| 提示词配置 | 继续复用现有复刻提示词后台配置体系 |
| `EcomPromptBuilder` | **不改**，继续接收一个 `Long categoryCode` 参数 |

### 1.3 关键设计原则

1. 顶部商品分类是**非必选项**
2. 顶部商品分类一旦有值，就是单图复刻创作命中分类提示词的**唯一来源**
3. `mallCategoryId` 为空时，直接走默认提示词前后缀，**不再回退到已选商品所属分类**
4. 已选商品仍然保留，只承担商品图来源、商品信息展示和商品权限校验等职责
5. 为了保证“返回任务”稳定回显，任务表除了保存分类 ID，还要保存分类名称快照

### 1.4 核心数据流变化

```text
改前：
  submitGenerateTask
    → validateAndGetProduct(productId)
    → product.getCategoryCode()
    → EcomPromptBuilder.buildReplicate(..., categoryCode, ...)

改后：
  submitGenerateTask
    → validateAndGetProduct(productId)               // 仅做商品权限与商品图能力承接
    → validateAndGetMallCategory(mallCategoryId)     // 顶部分类合法性校验
    → EcomPromptBuilder.buildReplicate(..., mallCategoryId, ...)
```

说明：

1. `mallCategoryId` 来自页面顶部商品分类
2. `mallCategoryId` 为空时，`EcomPromptBuilder` 会自然回退默认提示词前后缀
3. 不再引入“`mallCategoryId` 为空时，再用商品分类补一次”的兼容逻辑

---

## 二、数据库设计

### 2.1 `ecom_single_image_task` 新增字段

本次建议新增两个字段：

```sql
ALTER TABLE `ecom_single_image_task`
    ADD COLUMN `mall_category_id` BIGINT DEFAULT NULL
        COMMENT '页面顶部商城分类ID，来源于商城分类可选项'
        AFTER `product_id`,
    ADD COLUMN `mall_category_name` VARCHAR(100) DEFAULT NULL
        COMMENT '页面顶部商城分类名称快照'
        AFTER `mall_category_id`;
```

设计说明：

1. `mall_category_id`
   - 保存用户本次任务最终生效的顶部商品分类
   - 允许为空：顶部分类不是必选项
2. `mall_category_name`
   - 保存提交时的分类名称快照
   - 用于“返回任务”稳定回显，避免后续分类被删除、停用或改名后无法还原历史语义
3. 不加索引
   - 当前没有按该字段做列表筛选的需求
4. 不加外键
   - 与当前 `product_id` 保持同一策略

### 2.2 迁移脚本

新建文件：

- `src/main/resources/db/migration/V20260512.001__add_mall_category_fields_to_single_image_task.sql`

---

## 三、后端方案设计

### 3.1 `EcomGenerateRequest`（DTO）

新增字段：

```java
@Schema(description = "页面顶部商品分类ID；仅 replicate 模式生效")
private Long mallCategoryId;
```

说明：

1. 类型：`Long`
2. 非必填
3. 仅 `replicate` 模式使用
4. `single` 模式即使误传，后端也忽略，不落库、不参与 prompt

### 3.2 `EcomSingleImageTask`（Entity）

新增字段：

```java
@TableField("mall_category_id")
@Schema(description = "页面顶部商品分类ID")
private Long mallCategoryId;

@TableField("mall_category_name")
@Schema(description = "页面顶部商品分类名称快照")
private String mallCategoryName;
```

### 3.3 `EcomTaskContextVO`（VO）

新增字段：

```java
@Schema(description = "页面顶部商品分类ID")
private Long mallCategoryId;

@Schema(description = "页面顶部商品分类名称")
private String mallCategoryName;
```

### 3.4 `EcomSingleImageServiceImpl`（核心改动）

#### 3.4.1 `submitGenerateTask`

现有代码位置：

- `src/main/java/com/jinsui/business/ecom/service/impl/EcomSingleImageServiceImpl.java`

当前主流程里已有：

1. `validateGenerateRequest(request)`
2. `validateAndGetProduct(request.getProductId(), hostUuid)`
3. `buildFinalPrompt(creationType, request, product)`
4. 任务主表入库

本次改造后，单图复刻创作流程调整为：

```text
submitGenerateTask
  → validateGenerateRequest(request)
  → resolveCreationType(request.getCreationType())
  → validateAndGetProduct(request.getProductId(), hostUuid)          // 保留
  → validateAndGetMallCategory(request.getMallCategoryId(), hostUuid) // 新增，仅 replicate 使用
  → buildFinalPrompt(creationType, request, mallCategoryId)
  → 任务主表保存 mallCategoryId + mallCategoryName 快照
```

#### 3.4.2 `validateAndGetMallCategory`

建议在 `EcomSingleImageServiceImpl` 中新增私有方法：

```java
private EcomMallCategory validateAndGetMallCategory(Long mallCategoryId, String currentHostUuid) {
    if (mallCategoryId == null) {
        return null;
    }
    if (!StringUtils.hasText(currentHostUuid)) {
        throw new ApiException("商品分类不存在或无权限访问");
    }
    EcomMallCategory category = ecomMallCategoryMapper.selectOne(
            new LambdaQueryWrapper<EcomMallCategory>()
                    .eq(EcomMallCategory::getId, mallCategoryId)
                    .eq(EcomMallCategory::getHostUuid, currentHostUuid)
                    .eq(EcomMallCategory::getStatus, 1)
                    .eq(EcomMallCategory::getLevel, 1)
                    .last("limit 1")
    );
    if (category == null) {
        throw new ApiException("商品分类不存在或不可用");
    }
    return category;
}
```

校验目的：

1. 防止前端传入当前主机不可见的分类
2. 防止传入停用分类
3. 防止传入不属于当前页面这批“二级分类”口径的其他层级分类

说明：

- 这里直接复用 `ecom_mall_category` 现有数据，不新增新表
- 当前商城分类模块里，页面可选这批分类对应 `level = 1`、`status = 1`

#### 3.4.3 `buildFinalPrompt`

当前实现中，单图复刻创作会从 `product.getCategoryCode()` 取分类再传给 `EcomPromptBuilder.buildReplicate(...)`。

本次改为：

```java
private String buildFinalPrompt(String creationType,
                                EcomGenerateRequest request,
                                Long mallCategoryId) {
    EcomTextModificationDTO textMod = request.getTextModifications();
    if (CREATION_TYPE_REPLICATE.equals(creationType)) {
        return ecomPromptBuilder.buildReplicate(
                request.getPrompt(),
                request.getPolishedPrompt(),
                mallCategoryId,
                textMod
        );
    }
    return ecomPromptBuilder.build(request.getPrompt(), request.getPolishedPrompt(), textMod);
}
```

关键决策：

1. `mallCategoryId` 有值时直接使用
2. `mallCategoryId` 为空时直接传 `null`
3. **不再回退到商品分类**
4. `null` 交给 `EcomPromptBuilder` 按现有逻辑走默认提示词前后缀

#### 3.4.4 任务主表入库

在 `replicate` 分支中，任务入库时保存：

```java
task.setMallCategoryId(mallCategory == null ? null : mallCategory.getId());
task.setMallCategoryName(mallCategory == null ? null : mallCategory.getCategoryName());
```

对 `single` 模式：

1. 不做商城分类校验
2. 不保存 `mallCategoryId`
3. 不保存 `mallCategoryName`

### 3.5 `EcomPromptBuilder`（不改）

当前 `EcomPromptBuilder.buildReplicate(..., Long categoryCode, ...)` 已满足本次需求，不需要改动。

前提是当前业务中：

- 页面顶部可选分类使用的分类标识
- 复刻提示词配置使用的分类标识

处于同一数值域。

也就是说，本次设计依赖一个业务约束：

```text
页面顶部分类标识 = 复刻提示词分类标识
```

如果未来这两个标识体系分离，再单独引入映射层。

### 3.6 `EcomCreationRecordServiceImpl`（返回任务回填）

当前 `getSingleTaskContext` 已负责回填：

- `prompt`
- `polishedPrompt`
- `textModifications`
- `resolution`
- `ratio`
- `productId`
- 图片与结果图

本次新增回填：

```java
context.setMallCategoryId(task.getMallCategoryId());
context.setMallCategoryName(task.getMallCategoryName());
```

设计说明：

1. 直接使用任务表快照
2. 不在返回任务时再实时查商城分类表
3. 因此即使分类被改名、停用或删除，历史任务仍能稳定回显当时的分类名称

### 3.7 `EcomSingleImageController / EcomSingleImageServiceImpl`（页面再次进入初始化回显）

新增接口：

- `GET /api/ecommerce/single-image/replicate/init-config`

返回结构：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "mallCategoryId": 2001,
    "mallCategoryName": "内裤"
  }
}
```

后端处理口径：

1. 只查询当前登录用户最近一次成功创建的 `replicate` 任务
2. 这里的“上次”只以“提交成功创建了任务”为准，不要求任务最终生成成功
3. 查询时不能额外过滤 `mall_category_id is not null`
4. 因此如果最近一次复刻任务当时没选分类，则接口返回空回显
5. 不允许跳过最近一次空值任务，再回退到更早的一次有分类任务
6. 直接返回任务表里的分类快照，不实时反查商城分类表

示例查询逻辑：

```java
selectOne(
    where deleted = 0
      and user_uuid = currentUserUuid
      and creation_type = 'replicate'
    order by create_time desc, id desc
    limit 1
)
```

设计说明：

1. 该接口只服务“重新进入新建页时回显上次提交状态”
2. 它和“返回任务”是两条不同的数据链路，不要混用
3. 前端拿到 `mallCategoryId` 后，再和当前 `GET /api/mall/category/enabled/levelone` 的可选项做匹配
4. 如果当前分类列表里不存在该 `mallCategoryId`，前端保持未选中即可

### 3.8 “上传商品图”弹窗的后端承接

这块需要分开看。

#### 3.8.1 商品分页查询接口

商品选择弹窗当前复用商品分页查询能力，后端已有按分类过滤的支持：

- 请求 DTO：`EcomProductPageRequest.categoryCode`
- Service 过滤：`EcomProductServiceImpl.buildPageQuery(...)`

当前实现已经存在：

```java
if (request.getCategoryCode() != null) {
    queryWrapper.eq(EcomProduct::getCategoryCode, request.getCategoryCode());
}
```

因此：

1. 顶部已选分类时，前端把该分类作为商品分页查询条件传给现有商品接口即可
2. 这部分后端**无需新增接口，也无需新增查询逻辑**

#### 3.8.2 选择已有商品后的分类回填

选择已有商品后，前端需要判断该商品所属分类是否存在于顶部可选分类中，再决定是否回填顶部分类。

这条链路的后端承接也基本现成：

1. 商品分页返回中已有：
   - `categoryCode`
   - `categoryName`
2. 顶部分类数据源里已有当前页面可选分类集合

因此：

1. “是否回填顶部分类”的判断逻辑主要在前端
2. 后端不需要为“回填动作”新增专门接口
3. 后端只需要确保商品分页返回里继续稳定带出商品分类信息

---

## 四、不改动的代码

| 文件 | 原因 |
|---|---|
| `EcomPromptBuilder` | 现有 `buildReplicate` 签名可复用 |
| `EcomImageGenerateTask` | 异步生图只消费已冻结的 `finalPrompt` |
| `EcomSingleImageTaskScheduler` | 补偿逻辑与分类无关 |
| `EcomCreationRecordController` | 继续透传 taskId/context 请求即可 |
| 商品分页查询接口本身 | 已支持分类过滤，不需要新增新接口 |
| 套图相关代码 | 需求明确不涉及 |

---

## 五、兼容性与发布策略

### 5.1 旧版前端未传顶部分类

本次设计**不保留**“回退到已选商品分类”的旧逻辑。

因此如果旧版前端不传顶部分类：

1. 复刻创作仍可正常提交
2. 但分类提示词会走默认提示词前后缀
3. 不会再按已选商品所属分类命中分类提示词

结论：

- 这是一个有意为之的行为切换
- 需要前后端联动上线
- 最好由前端先具备传递顶部分类的能力，再切后端逻辑

### 5.2 存量任务

历史任务没有 `mall_category_id` / `mall_category_name` 时：

1. 返回任务上下文里这两个字段为空
2. 前端顶部商品分类保持不选中即可
3. 不影响历史任务的其他上下文恢复

### 5.3 分类后续被改名或删除

因为任务表保存了分类名称快照：

1. 返回任务仍可展示当时提交时使用的分类名称
2. 不依赖实时查商城分类表
3. 不影响已经冻结的 `finalPrompt`

---

## 六、改动文件清单

| 文件 | 改动类型 | 说明 |
|---|---|---|
| `EcomGenerateRequest.java` | 改 | 新增 `mallCategoryId` 字段 |
| `EcomSingleImageTask.java` | 改 | 新增 `mallCategoryId`、`mallCategoryName` 字段 |
| `EcomSingleImageController.java` | 改 | 新增复刻页初始化回显接口 |
| `EcomSingleImageService.java` | 改 | 新增复刻页初始化查询方法 |
| `EcomSingleImageServiceImpl.java` | 改 | 增加商城分类校验、分类来源切换、任务快照持久化 |
| `EcomSingleImageReplicateInitVO.java` | 新增 | 复刻页初始化回显响应 |
| `EcomTaskContextVO.java` | 改 | 新增 `mallCategoryId`、`mallCategoryName` 字段 |
| `EcomCreationRecordServiceImpl.java` | 改 | `getSingleTaskContext` 回填顶部分类快照 |
| `V20260512.001__add_mall_category_fields_to_single_image_task.sql` | 新增 | 数据库迁移脚本 |

---

## 七、测试要点

### 7.1 单元测试

| 场景 | 预期 |
|---|---|
| replicate + 顶部分类已选 + 分类提示词配齐 | `finalPrompt` 命中对应分类提示词 |
| replicate + 顶部分类已选 + 分类提示词未配齐 | `finalPrompt` 回退默认提示词前后缀 |
| replicate + 顶部分类为空 + 已选商品存在 | 不回退商品分类，直接走默认提示词前后缀 |
| replicate + 顶部分类为空 + 未选商品 | 走默认提示词前后缀 |
| replicate + 顶部分类已选 + 已选商品分类不一致 | 仍以顶部分类为准 |
| replicate + 顶部分类传入非法值（其他主机/停用/非当前层级） | 直接拒绝提交 |
| single + 误传顶部分类 | 忽略该字段，不参与 prompt，也不落库 |
| 重新进入复刻页 + 最近一次复刻任务有顶部分类 | 初始化接口返回最近一次任务保存的 `mallCategoryId/mallCategoryName` |
| 重新进入复刻页 + 最近一次复刻任务未选择分类 | 初始化接口返回空，不回退更早一次有分类的任务 |
| 返回任务 + 有顶部分类快照 | `context` 正确回填顶部分类 ID 和名称 |
| 返回任务 + 无顶部分类快照 | `context` 中顶部分类字段为空 |

### 7.2 联调验证

1. 顶部已选分类，打开“上传商品图”弹窗，商品查询结果按该分类过滤
2. 顶部未选分类，选择已有商品且该商品分类在顶部可选项中，前端可正确回填顶部分类
3. 顶部未选分类，选择已有商品但该商品分类不在顶部可选项中，不回填，提交走默认提示词
4. 顶部未选分类，本地上传商品图，提交走默认提示词
5. 顶部已选分类，本地上传商品图，提交按顶部分类命中分类提示词
6. 提交成功后，检查任务表中的顶部分类 ID 与名称快照
7. 重新进入复刻新建页，确认会按最近一次复刻任务回显顶部分类；若最近一次没选分类，则保持未选
8. 从创作记录返回任务，确认顶部分类回显正确
