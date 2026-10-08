import assert from 'node:assert/strict'
import { once } from 'node:events'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { createServer as createHttpServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { createServer, preview } from 'vite'

for (const mode of ['dev', 'preview'] as const) {
  test(`MCP requests and streaming responses pass through Vite ${mode}`, { timeout: 20_000 }, async () => {
    let releaseStream: (() => void) | undefined
    const upstream = createHttpServer(async (req, res) => {
      if (req.headers.authorization !== 'Bearer test-token') {
        res.writeHead(401, { 'Content-Type': 'application/json' }).end('{"error":"unauthorized"}')
        return
      }
      res.setHeader('Mcp-Session-Id', 'test-session')
      if (req.headers.accept === 'text/event-stream') {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' })
        await new Promise<void>((resolve) => {
          releaseStream = resolve
          res.write('event: message\ndata: first\n\n')
        })
        res.end('event: message\ndata: last\n\n')
        return
      }
      let body = ''
      for await (const chunk of req) body += chunk
      res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({
        method: req.method, path: req.url, body,
        session: req.headers['mcp-session-id'],
        protocol: req.headers['mcp-protocol-version'],
      }))
    })
    upstream.listen(0, '127.0.0.1')
    await once(upstream, 'listening')
    const root = await mkdtemp(join(tmpdir(), 'weknora-mcp-proxy-'))
    const previousTarget = process.env.VITE_DEV_PROXY_TARGET
    process.env.VITE_DEV_PROXY_TARGET = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}`
    let closeGateway: (() => Promise<void>) | undefined
    try {
      await mkdir(join(root, 'dist'))
      await writeFile(join(root, 'index.html'), 'main SPA')
      await writeFile(join(root, 'dist/index.html'), 'main SPA')
      const config = {
        configFile: fileURLToPath(new URL('./vite.config.ts', import.meta.url)),
        root,
        logLevel: 'silent' as const,
      }
      let port: number
      if (mode === 'dev') {
        const gateway = await createServer({
          ...config,
          server: { host: '127.0.0.1', port: 0, watch: null },
          optimizeDeps: { noDiscovery: true, include: [] },
        })
        closeGateway = () => gateway.close()
        await gateway.listen()
        port = (gateway.httpServer!.address() as AddressInfo).port
      } else {
        const gateway = await preview({ ...config, preview: { host: '127.0.0.1', port: 0 } })
        closeGateway = () => new Promise<void>((resolve, reject) => {
          gateway.httpServer.close((error) => error ? reject(error) : resolve())
          gateway.httpServer.closeAllConnections()
        })
        port = (gateway.httpServer.address() as AddressInfo).port
      }
      const url = `http://127.0.0.1:${port}/mcp/test-endpoint?probe=1`
      const headers = {
        Authorization: 'Bearer test-token',
        'Mcp-Session-Id': 'test-session',
        'MCP-Protocol-Version': '2025-03-26',
        'Content-Type': 'application/json',
      }
      for (const method of ['POST', 'GET', 'DELETE']) {
        const body = method === 'POST' ? '{"jsonrpc":"2.0","id":1,"method":"initialize"}' : undefined
        const response = await fetch(url, { method, headers, body, signal: AbortSignal.timeout(5000) })
        assert.equal(response.status, 200)
        assert.equal(response.headers.get('Mcp-Session-Id'), 'test-session')
        assert.deepEqual(await response.json(), {
          method, path: '/mcp/test-endpoint?probe=1', body: body ?? '',
          session: 'test-session', protocol: '2025-03-26',
        })
      }
      const unauthorized = await fetch(url, { signal: AbortSignal.timeout(5000) })
      assert.equal(unauthorized.status, 401)
      assert.deepEqual(await unauthorized.json(), { error: 'unauthorized' })
      const response = await fetch(url, {
        headers: { ...headers, Accept: 'text/event-stream' }, signal: AbortSignal.timeout(5000),
      })
      assert.equal(response.headers.get('Content-Type'), 'text/event-stream')
      const reader = response.body!.getReader()
      // The backend cannot finish until the first event reaches the client.
      assert.equal(new TextDecoder().decode((await reader.read()).value), 'event: message\ndata: first\n\n')
      releaseStream!()
      assert.equal(new TextDecoder().decode((await reader.read()).value), 'event: message\ndata: last\n\n')
      assert.equal((await reader.read()).done, true)
    } finally {
      releaseStream?.()
      await closeGateway?.()
      upstream.closeAllConnections()
      await new Promise<void>((resolve) => upstream.close(() => resolve()))
      if (previousTarget === undefined) delete process.env.VITE_DEV_PROXY_TARGET
      else process.env.VITE_DEV_PROXY_TARGET = previousTarget
      await rm(root, { recursive: true, force: true })
    }
  })
}
