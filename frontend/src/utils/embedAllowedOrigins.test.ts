import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { validateAllowedOrigins } from './embedAllowedOrigins'

test('accepts host origins, ports, trailing slash and subdomain patterns', () => {
 for (const origin of ['https://shop.example.com', 'http://localhost:8080', 'https://shop.example.com/', '*.example.com', '*.example.com:8443']) {
  assert.equal(validateAllowedOrigins([origin], true).ok, true, origin)
 }
})

test('rejects URLs that cannot be used as origin/CSP sources', () => {
 for (const origin of ['https://shop.example.com/path', 'https://user@shop.example.com', 'https://shop.example.com?q=1', 'https://shop.example.com#', 'https://shop.example.com;', 'https://shop.example.com; frame-ancestors *', 'https://*', 'https://shop.example.com/../']) {
  assert.equal(validateAllowedOrigins([origin], true).ok, false, origin)
 }
 assert.equal(validateAllowedOrigins([], true).ok, false)
 assert.equal(validateAllowedOrigins(['*'], true).ok, false)
 assert.equal(validateAllowedOrigins(['*'], false).ok, true)
})
