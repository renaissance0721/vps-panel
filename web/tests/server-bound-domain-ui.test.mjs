import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')

test('服务器创建、编辑和详情都提供绑定域名字段且列表不新增列', async () => {
  const [createForm, editForm, detail, list, composable, types] = await Promise.all([
    source('components/server/ServerForm.vue'),
    source('components/server/ServerBasicInfoForm.vue'),
    source('components/server/ServerDetail.vue'),
    source('components/server/ServerList.vue'),
    source('composables/useServers.ts'),
    source('types/server.ts'),
  ])
  assert.match(createForm, /已绑定域名（可选）[\s\S]*createServerBoundDomain/)
  assert.match(editForm, /已绑定域名（可选）[\s\S]*boundDomainInput/)
  assert.match(detail, /<dt>已绑定域名<\/dt><dd>\{\{ selectedServer\.bound_domain \|\| '未设置' \}\}<\/dd>/)
  assert.doesNotMatch(list, /<th>绑定域名<\/th>/)
  assert.match(composable, /body: JSON\.stringify\(\{ bound_domain: boundDomain \}\)/)
  assert.match(types, /bound_domain: string/)
})

test('Proxy 表单提供绑定域名、自动检测和手动输入三种 UI 选择', async () => {
  const [form, composable] = await Promise.all([
    source('components/proxy/ProxyForm.vue'),
    source('composables/useProxyForm.ts'),
  ])
  assert.match(form, /value="bound">已绑定域名：\{\{ selectedServerBoundDomain \}\}/)
  assert.match(form, /value="auto">自动检测/)
  assert.match(form, /value="manual">手动输入/)
  assert.match(form, /@change="onProxyServerChange"/)
  assert.match(composable, /value\.entry_host_mode === 'manual' && selectedServerBoundDomain\.value && value\.entry_host === selectedServerBoundDomain\.value/)
})

test('表格操作单元格保持 table-cell，按钮由内部 flex 容器布局', async () => {
  const paths = [
    'components/proxy/ClientList.vue',
    'components/proxy/ExternalNodeManager.vue',
    'components/proxy/ProxyList.vue',
    'components/server/ServerList.vue',
    'views/RelaysView.vue',
  ]
  const [style, ...files] = await Promise.all([source('style.css'), ...paths.map(source)])
  assert.match(style, /td\.server-actions\s*{[^}]*display:\s*table-cell;[^}]*vertical-align:\s*middle;/s)
  assert.match(style, /\.server-action-buttons\s*,?[^}]*display:\s*flex;[^}]*flex-wrap:\s*wrap;/s)
  assert.match(style, /\.server-action-buttons\s*{[^}]*align-items:\s*center;/s)
  assert.match(style, /\.client-table \.server-action-buttons\s*{[^}]*gap:\s*6px;/s)
  for (let index = 0; index < files.length; index++) {
    const cells = files[index].match(/<td class="server-actions">/g) ?? []
    const wrapped = files[index].match(/<td class="server-actions">\s*<div class="server-action-buttons">/g) ?? []
    assert.ok(cells.length > 0, `${paths[index]} should contain action cells`)
    assert.equal(wrapped.length, cells.length, `${paths[index]} action cells should all use an inner wrapper`)
  }
})

test('新增服务器双列表单从顶部对齐且小屏仍为单列', async () => {
  const style = await source('style.css')
  assert.match(style, /\.server-create-section\s*{[^}]*grid-template-columns:\s*repeat\(2, minmax\(0, 1fr\)\);[^}]*align-items:\s*start;/s)
  assert.match(style, /\.server-create-section > label\s*{[^}]*align-self:\s*start;[^}]*align-content:\s*start;[^}]*width:\s*100%;/s)
  assert.match(style, /@media \(max-width: 640px\)[\s\S]*\.server-create-section\s*{[^}]*grid-template-columns:\s*1fr;/s)
})
