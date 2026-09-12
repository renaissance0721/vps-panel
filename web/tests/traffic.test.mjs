import assert from 'node:assert/strict'
import test from 'node:test'

import { ref } from 'vue'

import {
  GIBIBYTE,
  TEBIBYTE,
  formatTrafficLimitInput,
  normalizeTrafficResetTime,
  parseTrafficLimit,
  trafficWarningLevel,
  useTrafficForm,
} from '../src/traffic.ts'

test('月流量额度默认使用 G', () => {
  assert.deepEqual(formatTrafficLimitInput(null), { value: '', unit: 'G' })
})

test('G 和 T 正确转换为字节', () => {
  assert.equal(parseTrafficLimit('500', 'G'), 500 * GIBIBYTE)
  assert.equal(parseTrafficLimit('1', 'T'), TEBIBYTE)
  assert.equal(parseTrafficLimit('2', 'T'), 2 * TEBIBYTE)
  assert.equal(parseTrafficLimit(500, 'G'), 500 * GIBIBYTE)
  assert.equal(parseTrafficLimit(1, 'T'), TEBIBYTE)
})

test('流量校准解析接受 number 输入', () => {
  assert.equal(parseTrafficLimit(183, 'G'), 183 * GIBIBYTE)
})

test('已有额度优先使用整 T，否则使用 G', () => {
  assert.deepEqual(formatTrafficLimitInput(2 * TEBIBYTE), { value: '2', unit: 'T' })
  assert.deepEqual(formatTrafficLimitInput(500 * GIBIBYTE), { value: '500', unit: 'G' })
})

test('流量预警覆盖固定边界', () => {
  const limit = 500 * GIBIBYTE
  assert.equal(trafficWarningLevel(38 * GIBIBYTE, null), null)
  assert.equal(trafficWarningLevel(320 * GIBIBYTE, limit), null)
  assert.equal(trafficWarningLevel(450 * GIBIBYTE, limit), 'warning')
  assert.equal(trafficWarningLevel(460 * GIBIBYTE, limit), 'warning')
  assert.equal(trafficWarningLevel(500 * GIBIBYTE, limit), 'exhausted')
  assert.equal(trafficWarningLevel(620 * GIBIBYTE, limit), 'exhausted')
})

test('流量预警不会改变服务器状态', () => {
  for (const status of ['pending', 'online', 'offline']) {
    const server = { status }
    assert.equal(trafficWarningLevel(460 * GIBIBYTE, 500 * GIBIBYTE), 'warning')
    assert.equal(server.status, status)
  }
})

test('流量预警使用 API 返回的校准后已用流量', () => {
  const limit = 500 * GIBIBYTE
  const warningResponse = { traffic_used_bytes: 460 * GIBIBYTE }
  const exhaustedResponse = { traffic_used_bytes: 620 * GIBIBYTE }

  assert.equal(trafficWarningLevel(warningResponse.traffic_used_bytes, limit), 'warning')
  assert.equal(trafficWarningLevel(exhaustedResponse.traffic_used_bytes, limit), 'exhausted')
})

test('月流量表单校验错误显示在当前表单且不发送请求', async (t) => {
  const cases = [
    {
      name: '非法月流量额度',
      change: (form) => { form.trafficLimitInput.value = 'invalid' },
      error: '月流量额度格式无效，请输入大于 0 的数值，或留空表示不限',
    },
    {
      name: '非法重置日',
      change: (form) => { form.trafficResetDay.value = 32 },
      error: '流量重置日必须在 1–31 之间',
    },
    {
      name: '非法重置时间',
      change: (form) => { form.trafficResetTime.value = '24:00' },
      error: '流量重置时间格式无效',
    },
  ]

  for (const value of cases) {
    await t.test(value.name, async () => {
      let requests = 0
      const { form } = createTrafficForm(async (server) => {
        requests++
        return server
      })
      form.openTrafficModal()
      value.change(form)
      await form.saveTrafficConfig()
      assert.equal(form.trafficFormError.value, value.error)
      assert.equal(form.trafficModalOpen.value, true)
      assert.equal(requests, 0)
    })
  }
})

test('月流量 PATCH 错误显示在 Modal 内', async () => {
  const { form, submitting } = createTrafficForm(async () => {
    throw new Error('服务器拒绝更新（500）')
  })
  form.openTrafficModal()
  await form.saveTrafficConfig()
  assert.equal(form.trafficFormError.value, '服务器拒绝更新（500）')
  assert.equal(form.trafficModalOpen.value, true)
  assert.equal(submitting.value, false)
})

test('合法月流量设置发送更新并在成功后关闭 Modal', async () => {
  let request
  let loads = 0
  const updated = trafficServer({
    monthly_traffic_limit_bytes: 500 * GIBIBYTE,
    traffic_count_mode: 'bidirectional',
    traffic_reset_day: 15,
    traffic_reset_time: '08:30',
  })
  const { form, selectedServer, submitting } = createTrafficForm(async (_server, serverID, payload) => {
    request = { serverID, payload }
    return updated
  }, async () => { loads++ })
  form.openTrafficModal()
  form.trafficLimitInput.value = 500
  form.trafficCountMode.value = 'bidirectional'
  form.trafficResetDay.value = 15
  form.trafficResetTime.value = '08:30'

  await form.saveTrafficConfig()

  assert.deepEqual(request, {
    serverID: 7,
    payload: {
      monthly_traffic_limit_bytes: 500 * GIBIBYTE,
      traffic_count_mode: 'bidirectional',
      traffic_reset_day: 15,
      traffic_reset_time: '08:30',
    },
  })
  assert.deepEqual(selectedServer.value, updated)
  assert.equal(form.trafficModalOpen.value, false)
  assert.equal(form.trafficFormError.value, '')
  assert.equal(submitting.value, false)
  assert.equal(loads, 1)
})

test('旧格式重置时间回填为 HH:MM 并可直接保存', async () => {
  let payload
  const { form } = createTrafficForm(async (server, _serverID, value) => {
    payload = value
    return server
  }, undefined, trafficServer({ traffic_reset_time: '00:00:00' }))
  form.openTrafficModal()
  assert.equal(form.trafficResetTime.value, '00:00')
  await form.saveTrafficConfig()
  assert.equal(payload.traffic_reset_time, '00:00')
  assert.equal(normalizeTrafficResetTime('23:59:59'), '23:59')
  assert.equal(normalizeTrafficResetTime('24:00:00'), '24:00:00')
})

function trafficServer(overrides = {}) {
  return {
    id: 7,
    monthly_traffic_limit_bytes: null,
    traffic_count_mode: 'single',
    traffic_reset_day: 1,
    traffic_reset_time: '00:00',
    ...overrides,
  }
}

function createTrafficForm(update, load = async () => {}, server = trafficServer()) {
  const selectedServer = ref(server)
  const submitting = ref(false)
  const form = useTrafficForm(
    selectedServer,
    submitting,
    (serverID, payload) => update(selectedServer.value, serverID, payload),
    load,
    () => {},
  )
  return { form, selectedServer, submitting }
}
