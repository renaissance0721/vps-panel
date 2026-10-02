import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { beginDragPreview, endDragPreview, edgeScrollVelocity, nearestScrollableAncestor } from '../src/drag.ts'

const originals = new Map()
function install(name, value) {
  if (!originals.has(name)) originals.set(name, Object.getOwnPropertyDescriptor(globalThis, name))
  Object.defineProperty(globalThis, name, { value, configurable: true, writable: true })
}
afterEach(() => {
  endDragPreview()
  for (const [name, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor)
    else delete globalThis[name]
  }
  originals.clear()
})

test('边缘速度随距离增加，中央、越界及滚动尽头停止，最后一帧不超界', () => {
  const velocity = (y, scroll = 500) => edgeScrollVelocity(y, 100, 700, scroll, 1000)
  assert.equal(velocity(400), 0)
  assert.ok(velocity(110) < velocity(150) && velocity(150) < 0)
  assert.ok(velocity(690) > velocity(650) && velocity(650) > 0)
  for (const y of [0, 701, NaN, Infinity]) assert.equal(velocity(y), 0)
  assert.equal(velocity(101, 0), 0)
  assert.equal(velocity(699, 1000), 0)
  assert.equal(velocity(100, 2), -2)
  assert.equal(velocity(700, 998), 2)
  assert.equal(velocity(100), -20)
  assert.equal(velocity(700), 20)
  assert.equal(edgeScrollVelocity(100, 100, 100, 500, 1000), 0)
})

function environment() {
  class Element {
    parentElement = null
    isConnected = true
    scrollTop = 0
    scrollHeight = 1800
    clientHeight = 600
    overflowY = 'visible'
    style = {}
    classes = new Set()
    classList = { add: v => this.classes.add(v), remove: v => this.classes.delete(v) }
    getBoundingClientRect() { return { top: 0, bottom: 600, left: 0, right: 900, width: 900, height: 600 } }
    cloneNode() { return new Element() }
    remove() { this.isConnected = false }
    append() {}
  }
  const page = new Element(), body = new Element(), source = new Element()
  body.parentElement = page
  source.parentElement = body
  const document = Object.assign(new EventTarget(), { scrollingElement: page, documentElement: page, body, createElement: () => new Element(), elementFromPoint: () => null })
  const window = Object.assign(new EventTarget(), { innerHeight: 600, innerWidth: 900 })
  const frames = new Map()
  let id = 0, observer
  install('document', document); install('window', window)
  install('HTMLTableRowElement', class {})
  install('DragEvent', class extends Event {
    constructor(type, options) { super(type, options); Object.assign(this, { clientX: options.clientX, clientY: options.clientY, relatedTarget: options.relatedTarget }) }
  })
  install('getComputedStyle', element => ({ overflowY: element.overflowY }))
  install('requestAnimationFrame', callback => { frames.set(++id, callback); return id })
  install('cancelAnimationFrame', id => frames.delete(id))
  install('MutationObserver', class {
    constructor(callback) { observer = this; this.callback = callback; this.connected = false }
    observe() { this.connected = true }
    disconnect() { this.connected = false }
  })
  function dispatch(type, values = {}) {
    const event = Object.assign(new Event(type, { cancelable: true }), values)
    document.dispatchEvent(event)
    return event
  }
  function tick() {
    assert.equal(frames.size, 1, 'only one pending RAF')
    const [key, callback] = [...frames][0]
    frames.delete(key); callback()
  }
  const event = { clientX: 200, clientY: 590, dataTransfer: { setData() {}, setDragImage() {} } }
  return { Element, document, page, body, source, frames, event, tick, dispatch, observer: () => observer }
}

test('只选择真正纵向可滚动祖先，跳过横向 wrapper 并回退页面', () => {
  const env = environment()
  env.body.overflowY = 'auto'; env.body.scrollHeight = env.body.clientHeight
  assert.equal(nearestScrollableAncestor(env.source), env.page)
  const modal = new env.Element()
  modal.overflowY = 'auto'; modal.parentElement = env.body; env.source.parentElement = modal
  assert.equal(nearestScrollableAncestor(env.source), modal)
  modal.overflowY = 'hidden'
  assert.equal(nearestScrollableAncestor(env.source), env.page)
})

test('窗口拖拽只有一个 RAF，dragover 不拦截事件，中央及尽头暂停，结束后无残留', () => {
  const env = environment()
  assert.equal(beginDragPreview({ dataTransfer: null }, env.source, '1'), false)
  assert.equal(env.frames.size, 0)
  assert.equal(beginDragPreview(env.event, env.source, '1'), true)
  for (let i = 0; i < 30; i++) assert.equal(env.dispatch('dragover', { clientX: 200, clientY: 590 }).defaultPrevented, false)
  env.tick(); assert.ok(env.page.scrollTop > 0)
  env.dispatch('dragover', { clientX: 200, clientY: 300 })
  const position = env.page.scrollTop
  env.tick(); assert.equal(env.page.scrollTop, position); assert.equal(env.frames.size, 0)
  env.page.scrollTop = 1200
  env.dispatch('dragover', { clientX: 200, clientY: 590 }); env.tick()
  assert.equal(env.frames.size, 0)
  env.dispatch('dragover', { clientX: 200, clientY: 10 }); env.tick()
  assert.ok(env.page.scrollTop < 1200)
  endDragPreview()
  assert.equal(env.frames.size, 0); assert.equal(env.observer().connected, false)
  assert.equal(env.source.classes.size, 0)
  env.dispatch('dragover', { clientX: 200, clientY: 590 })
  assert.equal(env.frames.size, 0, 'global listener removed')
})

test('内部容器按自身边缘滚动，离开范围停止，drop、dragend、ESC、源节点卸载均清理', () => {
  for (const finish of ['drop', 'dragend', 'Escape', 'unmount']) {
    const env = environment()
    env.body.overflowY = 'auto'; env.body.clientHeight = 300
    env.body.getBoundingClientRect = () => ({ top: 100, bottom: 400, left: 100, right: 800 })
    beginDragPreview({ ...env.event, clientY: 390 }, env.source, '1')
    env.tick(); assert.ok(env.body.scrollTop > 0); assert.equal(env.page.scrollTop, 0)
    env.dispatch('dragover', { clientX: 50, clientY: 390 }); env.tick()
    assert.equal(env.frames.size, 0)
    env.dispatch('dragover', { clientX: 200, clientY: 390 })
    if (finish === 'Escape') env.dispatch('keydown', { key: 'Escape' })
    else if (finish === 'unmount') { env.source.isConnected = false; env.observer().callback() }
    else env.dispatch(finish)
    assert.equal(env.frames.size, 0, finish)
    assert.equal(env.observer().connected, false, finish)
    assert.equal(env.source.classes.size, 0, finish)
  }
})

test('鼠标静止时滚动继续刷新新目标的 dragover，离开旧目标，但不生成 drop', () => {
  const env = environment()
  const first = new EventTarget(), second = new EventTarget()
  const events = []
  for (const [name, target] of [['first', first], ['second', second]]) {
    for (const type of ['dragover', 'dragleave', 'drop']) target.addEventListener(type, event => events.push([name, type, event.clientY]))
  }
  env.document.elementFromPoint = () => first
  beginDragPreview(env.event, env.source, '1')
  env.tick()
  env.document.elementFromPoint = () => second
  env.tick()
  assert.deepEqual(events.map(event => event.slice(0, 2)), [['first', 'dragover'], ['first', 'dragleave'], ['second', 'dragover']])
  assert.equal(events[0][2], 590)
  assert.equal(events[2][2], 590)
})

test('全部 HTML5 排序入口共用拖拽生命周期且卸载时清理', async () => {
  for (const path of ['components/server/ServerList.vue', 'components/proxy/ProxyList.vue', 'components/proxy/ExternalNodeManager.vue', 'views/RelaysView.vue', 'views/SubscriptionManagementView.vue', 'components/subscription/RoutingGroupEditor.vue', 'components/subscription/RoutingBindingEditor.vue']) {
    const source = await readFile(new URL(`../src/${path}`, import.meta.url), 'utf8')
    assert.match(source, /beginDragPreview\(event, source,/)
    assert.match(source, /onUnmounted\(/)
    assert.doesNotMatch(source, /requestAnimationFrame|scrollBy|scrollTop\s*=/)
  }
})
