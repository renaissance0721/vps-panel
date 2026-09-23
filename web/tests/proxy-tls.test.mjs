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

test('Proxy 表单按精确 capability 控制协议、安全层和 TLS 模式', async () => {
  const files = await Promise.all([
    'composables/useProxyForm.ts', 'components/proxy/ProxyForm.vue', 'components/proxy/TLSForm.vue', 'types/proxy.ts',
  ].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))
  const [form] = files
  const source = files.join('\n')
  assert.match(form, /agentCapabilities\.proxyVLESSReality/)
  assert.match(form, /agentCapabilities\.proxyVLESSTLSACME/)
  assert.match(form, /agentCapabilities\.proxyVLESSTLSManual/)
  assert.match(form, /agentCapabilities\.proxyShadowsocks/)
  assert.doesNotMatch(form, /'(?:proxy\.vless\.reality|proxy\.vless\.tls\.acme|proxy\.vless\.tls\.manual|proxy\.shadowsocks)'/)
  assert.match(source, /value="vless" :disabled="!proxyVLESSSupported"/)
  assert.match(source, /value="shadowsocks" :disabled="!proxyShadowsocksSupported"/)
  assert.match(source, /value="reality" :disabled="!proxyVLESSRealitySupported"/)
  assert.match(source, /value="tls" :disabled="!proxyTLSSupported"/)
  assert.match(source, /value="acme" :disabled="!proxyTLSACMESupported"/)
  assert.match(source, /value="manual" :disabled="!proxyTLSManualSupported"/)
  assert.match(source, /proxyEnabled && !!proxyCapabilityWarning/)
  assert.match(source, /当前 Agent 不支持 VLESS \+ TLS（手动证书）/)
})
