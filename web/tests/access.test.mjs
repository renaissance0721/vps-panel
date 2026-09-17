import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = (await Promise.all(["composables/useServers.ts","components/server/ServerForm.vue","components/server/ServerAccessForm.vue","components/server/ServerDetail.vue"].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))).join('\n')
const proxySource = await readFile(new URL('../src/composables/useProxies.ts', import.meta.url), 'utf8')
const relaySource = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')

test('服务器创建和详情支持公开/私有访问范围与账号选择', () => {
  assert.match(appSource, /visibility:\s*serverVisibility\.value/)
  assert.match(appSource, /user_ids:\s*serverVisibility\.value === 'private'/)
  assert.match(appSource, /公开（所有已登录账号）/)
  assert.match(appSource, /私有（仅指定账号）/)
  assert.match(appSource, /允许访问的账号/)
  assert.match(appSource, /修改访问范围/)
  assert.match(appSource, /\/api\/servers\/\$\{selectedServer\.value\.id\}\/access/)
  assert.match(appSource, /user\.id === state\?\.user\?\.id/)
})

test('服务器访问丢失后关闭详情且派生资源随可访问服务器刷新', () => {
  assert.match(appSource, /服务器不存在或当前账号无权访问/)
  assert.match(appSource, /serverModalOpen\.value = false/)
  assert.match(proxySource, /props\.servers\.map\(\(server\) => server\.id\)\.join\(','\)/)
  assert.match(proxySource, /代理节点不存在或当前账号无权访问/)
  assert.match(relaySource, /props\.servers\.map\(\(server\) => server\.id\)\.join\(','\)/)
  assert.match(relaySource, /中转规则不存在或当前账号无权访问/)
})
