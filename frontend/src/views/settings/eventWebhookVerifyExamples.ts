export type WebhookVerifyLang = 'java' | 'python' | 'go' | 'js' | 'curl'

export function webhookVerifyExample(lang: WebhookVerifyLang, downloadBase: string): string {
  const origin = (downloadBase || 'https://weknora.example.com').replace(/\/$/, '')
  const downloadHint = `${origin}/api/v1/files/knowledge-download/{id}`
  switch (lang) {
    case 'java':
      return javaExample(downloadHint)
    case 'python':
      return pythonExample(downloadHint)
    case 'go':
      return goExample(downloadHint)
    case 'js':
      return jsExample(downloadHint)
    case 'curl':
      return curlExample(downloadHint)
  }
}

function javaExample(downloadHint: string): string {
  return `// Spring Boot 3 / JDK 17+
// HMAC-SHA256(secret, timestamp + "." + raw_body)
// 必须用原始 body，不要先反序列化再 toJSON。
// 源文件：GET ${downloadHint}
// Header: X-WeKnora-Download-Ticket（5 分钟；过期 1 小时内可 POST .../renew）

package com.example.webhook;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.HexFormat;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class WeKnoraWebhookController {

  private static final long WINDOW_SECONDS = 300;

  @Value("\${weknora.webhook.secret}")
  private String secret;

  @PostMapping("/hooks/weknora")
  public ResponseEntity<Void> handle(
      @RequestHeader("X-WeKnora-Timestamp") String timestamp,
      @RequestHeader("X-WeKnora-Signature") String signature,
      @RequestBody byte[] rawBody) throws Exception {
    long ts = Long.parseLong(timestamp);
    if (Math.abs(Instant.now().getEpochSecond() - ts) > WINDOW_SECONDS) {
      return ResponseEntity.status(401).build();
    }
    Mac mac = Mac.getInstance("HmacSHA256");
    mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
    mac.update(timestamp.getBytes(StandardCharsets.UTF_8));
    mac.update((byte) '.');
    mac.update(rawBody);
    String expected = "sha256=" + HexFormat.of().formatHex(mac.doFinal());
    if (!MessageDigest.isEqual(
        expected.getBytes(StandardCharsets.UTF_8),
        signature.getBytes(StandardCharsets.UTF_8))) {
      return ResponseEntity.status(401).build();
    }
    // TODO: 把 JSON 丢进自己的队列，不要在这里同步下载文件
    return ResponseEntity.ok().build();
  }
}
`
}

function pythonExample(downloadHint: string): string {
  return `# Python 3.10+  仅标准库
# HMAC-SHA256(secret, timestamp + "." + raw_body)
# 必须用原始 body，不要 json.dumps 后再签。
# 源文件：GET ${downloadHint}
# Header: X-WeKnora-Download-Ticket（5 分钟；过期 1 小时内可 POST .../renew）

from http.server import BaseHTTPRequestHandler, HTTPServer
import hashlib
import hmac
import os
import time

SECRET = os.environ["WEKNORA_WEBHOOK_SECRET"].encode()
WINDOW = 300


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        ts = self.headers.get("X-WeKnora-Timestamp", "")
        sig = self.headers.get("X-WeKnora-Signature", "")
        try:
            unix = int(ts)
        except ValueError:
            self.send_error(401)
            return
        if abs(time.time() - unix) > WINDOW:
            self.send_error(401)
            return
        expected = "sha256=" + hmac.new(
            SECRET, f"{unix}.".encode() + raw, hashlib.sha256
        ).hexdigest()
        if len(sig) != len(expected) or not hmac.compare_digest(sig, expected):
            self.send_error(401)
            return
        # TODO: 把 JSON 丢进自己的队列，不要在这里同步下载文件
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", 8088), Handler).serve_forever()
`
}

function goExample(downloadHint: string): string {
  return `// Go 1.21+
// HMAC-SHA256(secret, timestamp + "." + raw_body)
// 必须用原始 body，不要 json.Marshal 后再签。
// 源文件：GET ${downloadHint}
// Header: X-WeKnora-Download-Ticket（5 分钟；过期 1 小时内可 POST .../renew）

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	secret := []byte(os.Getenv("WEKNORA_WEBHOOK_SECRET"))
	http.HandleFunc("/hooks/weknora", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		ts := r.Header.Get("X-WeKnora-Timestamp")
		sig := r.Header.Get("X-WeKnora-Signature")
		unix, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || abs(time.Now().Unix()-unix) > 300 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(ts))
		mac.Write([]byte("."))
		mac.Write(raw)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// TODO: 把 JSON 丢进自己的队列，不要在这里同步下载文件
		w.WriteHeader(http.StatusOK)
	})
	http.ListenAndServe(":8088", nil)
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
`
}

function jsExample(downloadHint: string): string {
  return `// Node.js 18+  ESM
// HMAC-SHA256(secret, timestamp + "." + raw_body)
// 必须用原始 Buffer，不要 JSON.stringify 后再签。
// 源文件：GET ${downloadHint}
// Header: X-WeKnora-Download-Ticket（5 分钟；过期 1 小时内可 POST .../renew）

import http from 'node:http'
import crypto from 'node:crypto'

const SECRET = process.env.WEKNORA_WEBHOOK_SECRET
if (!SECRET) throw new Error('WEKNORA_WEBHOOK_SECRET is required')

const server = http.createServer(async (req, res) => {
  if (req.method !== 'POST') {
    res.writeHead(405)
    res.end()
    return
  }
  const chunks = []
  for await (const chunk of req) chunks.push(chunk)
  const raw = Buffer.concat(chunks)
  const ts = String(req.headers['x-weknora-timestamp'] ?? '')
  const sig = String(req.headers['x-weknora-signature'] ?? '')
  const unix = Number(ts)
  if (!Number.isFinite(unix) || Math.abs(Date.now() / 1000 - unix) > 300) {
    res.writeHead(401)
    res.end()
    return
  }
  const expected = 'sha256=' + crypto
    .createHmac('sha256', SECRET)
    .update(\`\${unix}.\`)
    .update(raw)
    .digest('hex')
  const a = Buffer.from(sig)
  const b = Buffer.from(expected)
  if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) {
    res.writeHead(401)
    res.end()
    return
  }
  // TODO: 把 JSON 丢进自己的队列，不要在这里同步下载文件
  res.writeHead(200)
  res.end('ok')
})

server.listen(8088)
`
}

function curlExample(downloadHint: string): string {
  return `# WeKnora 实际 POST 的请求形态（对照接收端）
# HMAC-SHA256(secret, timestamp + "." + raw_body)
# 源文件：GET ${downloadHint}
# Header: X-WeKnora-Download-Ticket

curl -X POST "$URL" \\
  -H "Content-Type: application/json" \\
  -H "User-Agent: WeKnora-Workspace-Webhook/1.0" \\
  -H "X-WeKnora-Event: knowledge.created" \\
  -H "X-WeKnora-Delivery: dlv_..." \\
  -H "X-WeKnora-Timestamp: 1756624860" \\
  -H "X-WeKnora-Signature: sha256=<hmac>" \\
  -d '{"spec_version":"1","id":"evt_...","type":"knowledge.created"}'

# openssl 本地验签（raw 必须与 POST body 字节完全一致）
# printf '%s' "1756624860.$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex
`
}
