// Package handler 上传测试页（本地/联调用，浏览器可视化验证上传流程）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const uploadPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>视频上传测试</title>
<style>
  body { font-family: -apple-system, sans-serif; max-width: 640px; margin: 40px auto; padding: 0 20px; color: #1a1a1a; }
  h1 { font-size: 22px; }
  .field { margin: 16px 0; }
  label { display: block; font-size: 14px; margin-bottom: 6px; color: #555; }
  input[type=file], select { width: 100%; padding: 8px; border: 1px solid #ccc; border-radius: 6px; font-size: 14px; }
  button { padding: 10px 24px; background: #0052d9; color: #fff; border: none; border-radius: 6px; font-size: 15px; cursor: pointer; }
  button:disabled { background: #9ab; cursor: not-allowed; }
  pre { background: #f5f5f5; padding: 14px; border-radius: 6px; font-size: 12px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; }
  .ok { color: #0a7d32; }
  .err { color: #c00; }
</style>
</head>
<body>
<h1>视频上传测试</h1>
<p>选择视频文件 → 上传 → 后端自动入库并生成缩略图。</p>

<div class="field">
  <label>视频文件</label>
  <input type="file" id="file" accept="video/*">
</div>
<div class="field">
  <label>视频类型</label>
  <select id="type">
    <option value="training">training（培训）</option>
    <option value="interview">interview（访谈）</option>
    <option value="salon">salon（研讨）</option>
    <option value="general">general（通用）</option>
  </select>
</div>
<div class="field">
  <button id="btn" onclick="upload()">上传</button>
</div>

<pre id="log">就绪。</pre>

<script>
async function upload() {
  const fileInput = document.getElementById('file');
  const type = document.getElementById('type').value;
  const log = document.getElementById('log');
  const btn = document.getElementById('btn');

  if (!fileInput.files.length) { log.textContent = '❌ 请先选择视频文件'; return; }
  const file = fileInput.files[0];
  btn.disabled = true;

  log.textContent = '⏳ 上传中（文件名：' + file.name + '，大小：' + (file.size/1024/1024).toFixed(2) + ' MB）...\n';

  try {
    const fd = new FormData();
    fd.append('file', file);
    fd.append('video_type', type);

    const res = await fetch('/api/custom/uploads/direct', { method: 'POST', body: fd });
    const data = await res.json();

    if (!res.ok) {
      log.textContent = '❌ 上传失败（HTTP ' + res.status + '）\n' + JSON.stringify(data, null, 2);
      return;
    }

    log.textContent = '✅ 上传成功！\n\n' +
      '视频 ID：' + data.video_id + '\n' +
      '状态：' + data.status + '\n' +
      '缩略图任务：' + data.job_id + '\n\n' +
      '缩略图正在后台生成，稍等几秒可查：\n' +
      '  docker exec WeKnora-postgres psql -U postgres -d vidsage -c \\\n' +
      '    "SELECT id,status,thumbnail_url FROM videos WHERE id=\\'' + data.video_id + '\\''"';
  } catch (e) {
    log.textContent = '❌ 网络错误：' + e.message;
  } finally {
    btn.disabled = false;
  }
}
</script>
</body>
</html>
`

// uploadPage 返回上传测试页
func uploadPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(uploadPageHTML))
}
