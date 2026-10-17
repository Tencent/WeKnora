# 带密码的 Office 文件（docreader 解析失败）

本文记录「同步进来的 xlsx 解析失败」这一类问题的诊断结论与处理办法，样本来自 2026-09-20 的 OneDrive/SharePoint 数据源。

## 现象

数据源同步整体成功（`status=active`，`last_sync_at` 正常刷新），但个别条目 `parse_status=failed`、
`enable_status=disabled`，`error_message` 为：

```
Can't find workbook in OLE2 compound document
```

后端记为 `DOCREADER_PARSE_FAILED`（trace 的 `stage=docreader`）。该错误会在每次重试时重复记录一次，
不会让整个数据源同步失败。

## 结论

**这不是解析 bug，是文件本身带「打开密码」。** 没有密码，任何解析器都拿不到单元格数据。

实测样本：

| 项 | 值 |
|---|---|
| 文件 | `【MSP1少短】シーケンス（キャンセル保険）_V0.12.xlsx` |
| knowledge id | `5712d345-754f-4698-b3ef-0ea81d9b6230` |
| 存储 blob | `/alidata1/admin/10000/5712d345-754f-4698-b3ef-0ea81d9b6230/1789915478557177000.xlsx` |
| 大小 | 1,948,160 B |
| 容器类型 | OLE2 / CFB（魔数 `d0cf11e0a1b11ae1`） |
| 容器内流 | `EncryptionInfo` + `EncryptedPackage`，**没有** `Workbook` / `Book` |
| 加密算法 | agile v4.4，`<keyEncryptor uri="…/keyEncryptor/password">`，`spinCount=100000`，AES-256 / SHA-512 |

`EncryptionInfo` 里出现 `certificate` 字样只是 `xmlns:c` 命名空间声明，**不是**证书/IRM 加密，
是纯粹的密码加密。

需要区分三种容易混淆的情况：

- **打开密码**（本文讨论的）：容器里没有 `Workbook` 流，必须解密。
- **工作表保护 / 编辑保护**：文件结构正常，能打开，只是不能改。docreader 可以正常解析。
- **证书 / IRM 加密**：需要凭据管理服务，docreader 同样无法处理，但容器结构与本文不同。

## 判定方法

对任意一个存储 blob（或本地同名文件）跑：

```python
raw = open(path, "rb").read()
print(raw[:8].hex())                       # d0cf11e0a1b11ae1 即 OLE2/CFB
for name in ("EncryptionInfo", "EncryptedPackage", "Workbook", "Book"):
    print(name, name.encode("utf-16-le") in raw)
```

- `EncryptionInfo` + `EncryptedPackage` 为真、`Workbook` 为假 → 密码保护的 Office 文件。
- 注意写 `"EncryptionInfo".encode("utf-16-le")`（**str**），写成 `b"…".encode(...)` 会抛
  `AttributeError`；若外面还套了 `except Exception: pass`，症状就变成「扫描结果静默为 0」。

2026-09-20 对全部 203 个存储 blob 做过一次扫描：CFB 容器 **1** 个，其中带密码 **1** 个。
即该问题在当时的库内是孤例，不是批量性数据问题。

## 根因链路（为什么报错信息这么费解）

`docreader/parser/excel_convert.py`：

1. `:19 detect_excel_format()` 调用 pandas 的 `inspect_excel_format`，看到 CFB 魔数就返回 **`"xls"`**（`:15` `_XLS_MAGIC`、`:34-35`）。
   它分不清「老的 .xls」和「加密的 .xlsx」。
2. `:108 normalize_excel_bytes()` 见 `ext is not None` 便**原样返回**（`:110-112`），
   因此 `:49 convert_excel_to_xlsx_bytes()`（LibreOffice 兜底）**根本不会被调用**。
   `:127` 那条写着 "corrupt, encrypted" 的 `ValueError` 只在格式完全无法识别时才可能触发，这条路径走不到。
3. `docreader/parser/excel_parser.py:216` `engine_for_format("xls")` → `xlrd`；
   `:221 pd.ExcelFile(..., engine="xlrd")` 抛 `Can't find workbook in OLE2 compound document`。
   该异常不是 `ImportError` / `KeyError` / `ValueError`，所以 `:222-243` 的重试分支都接不住，直接冒泡成 `DOCREADER_PARSE_FAILED`。

即使走到 LibreOffice 兜底也一样：实测 `soffice` 对该文件报 `Error: source file could not be loaded`，不产出任何结果。

环境现状（`docreader/.venv`）：已有 `openpyxl`、`xlrd`、`pandas`、`pypdfium2` 和 LibreOffice；
**没有** `msoffcrypto`，也**没有** `olefile`。

## 处理方案

### 方案 A —— 源端去密码（零代码，首选）

在源端另存为不带密码的版本（Excel：文件 → 信息 → 保护工作簿 → 用密码进行加密 → 清空密码），
或本地解密一份副本后回传：

```bash
msoffcrypto-tool -p '<密码>' in.xlsx out.xlsx
```

重新同步会把它识别为变更项并重新解析，条目自动从 failed 转正。

顺带解决合规问题：密码会轮换，库里留一份「需要密码才能读」的密文没有意义。

### 方案 B —— 让失败可读 / 明确跳过（小改动）

当前行为（报错但不影响整个同步）已经足够安全，问题只是错误信息对用户毫无指导意义。
可以在 `detect_excel_format` 之后加一个廉价判断：**CFB 容器内扫 `EncryptedPackage` 的 UTF-16LE 字面量**，
命中即抛「该文件受密码保护，请移除密码后重新上传」。

这样不需要引入 `msoffcrypto` / `olefile` 依赖。落到代码上大致是：

- 在 `docreader/parser/excel_convert.py` 增加一个 `is_password_protected(content) -> bool`；
- 在 `_open_excel_file()` 进入 xlrd 之前判定，抛出带指导意义的 `ValueError`；
- 视需要新增一个 docreader 错误码（或复用 `DOCREADER_PARSE_FAILED`），前端/日志据此展示。

### 方案 C —— 真正支持带密码文件（改动最大，暂不建议）

解密本身不难（引入 `msoffcrypto-tool`），难点在**密码从哪来**：

- 环境变量：所有文件共用一套密码，现实中不成立；
- 数据源级配置：需用 `SYSTEM_AES_KEY` 加密存储，绝不明文入库、绝不写进日志；
- 上传/同步时人工输入：需要 UI 与任务重试流程配合。

在没有明确需求前，建议先做 A + B。

## 备注

- 本文覆盖的是 Excel 路径。PDF/Word 等格式可能有类似的加密分支（`pypdfium2` 本身支持 password 参数），
  但**未实测**，需要时按上面的判定方法重新确认。
- 存储路径由 `STORAGE_TYPE=local` + `LOCAL_STORAGE_BASE_DIR` 决定，布局为
  `<base>/<tenant_id>/<knowledge_id>/<纳秒时间戳>.<ext>`。
