import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

test('TLS 默认自动 ACME，只有手动模式才提交 PEM，旧证书按手动回填', async () => {
  const source = (await Promise.all(["composables/useProxyForm.ts","components/proxy/TLSForm.vue"].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))).join('\n')
  assert.match(source, /proxyTLSMode = ref<'acme' \| 'manual'>\('acme'\)/)
  assert.match(source, /proxyTLSMode\.value = value\.config\.tls_mode \?\? \(value\.config\.tls_certificate_configured \? 'manual' : 'acme'\)/)
  assert.match(source, /proxyTLSMode\.value = 'acme'/)
  assert.match(source, /proxyTLSMode\.value === 'manual' \? \{ certificate: proxyCertificate\.value, private_key: proxyPrivateKey\.value \} : \{\}/)
  assert.match(source, /v-if="proxyTLSMode === 'acme'"/)
  assert.match(source, /<template v-else>[\s\S]*?证书 PEM[\s\S]*?私钥 PEM/)
})
