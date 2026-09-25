import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { autofillExternalNodeName, externalNodeNameFromURI } from '../src/landing.ts'

test('外部节点名称从 URI fragment 解码', () => {
  assert.equal(externalNodeNameFromURI('vless://uuid@example.com:443#US-LAX'), 'US-LAX')
  assert.equal(externalNodeNameFromURI('vless://uuid@example.com:443#US%20Los%20Angeles'), 'US Los Angeles')
  assert.equal(externalNodeNameFromURI('ss://secret@example.com:8388#新加坡%20共享'), '新加坡 共享')
  assert.equal(externalNodeNameFromURI('vless://uuid@example.com:443'), '')
  assert.equal(externalNodeNameFromURI('vless://uuid@example.com:443#bad%2'), '')
})

test('外部节点名称只在当前名称为空时自动填充', () => {
  const uri = 'vless://uuid@example.com:443#US%20LAX'
  assert.equal(autofillExternalNodeName('', uri), 'US LAX')
  assert.equal(autofillExternalNodeName('我的节点', uri), '我的节点')
})

test('代理节点页同时展示受管节点和外部节点管理', async () => {
  const view = await readFile(new URL('../src/views/ProxiesView.vue', import.meta.url), 'utf8')
  const proxyList = await readFile(new URL('../src/components/proxy/ProxyList.vue', import.meta.url), 'utf8')
  const externalNodes = await readFile(new URL('../src/components/proxy/ExternalNodeManager.vue', import.meta.url), 'utf8')

  assert.match(view, /<ExternalNodeManager\s*\/>/)
  assert.match(proxyList, /<n-card[^>]*title="受管节点"[\s\S]*<template #header-extra>[\s\S]*proxy-header-search[\s\S]*新增代理节点[\s\S]*<div v-if="loading"/)
  assert.doesNotMatch(proxyList, /class="proxy-toolbar"/)
  assert.match(externalNodes, /title="外部节点"/)
  assert.match(externalNodes, />导入外部节点</)
  assert.match(externalNodes, /@click="showExternalNode\(value\)">查看/)
  assert.match(externalNodes, /value\.owned_by_me/)
  assert.match(externalNodes, /公开节点 · 仅所有者可编辑/)
  assert.doesNotMatch(externalNodes, /可作为中转目标使用/)
  assert.match(externalNodes, /公开外部节点后，其他 Panel 用户可以查看、复制完整节点链接并生成二维码/)
  assert.match(externalNodes, /name\.value = autofillExternalNodeName\(name\.value, value\)/)
  assert.match(externalNodes, /landingVisibilityLabel\(value\.visibility\)/)
  assert.match(externalNodes, /`\/api\/landings\/\$\{value\.id\}\/share`/)
  assert.match(externalNodes, /navigator\.clipboard\.writeText\(selectedShare\.value\.uri\)/)
  assert.match(externalNodes, /复制链接失败，请手动复制/)
  assert.match(externalNodes, /<QRCodeModal[^>]*:uri="qrURI"/)
  assert.match(externalNodes, /:value="selectedShare\.uri"[^>]*readonly/)
  assert.doesNotMatch(externalNodes, /<td>\s*{{\s*value\.uri/)
})
