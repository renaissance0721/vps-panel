import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const accountSource = await readFile(new URL('../src/components/AccountMenu.vue', import.meta.url), 'utf8')
const overviewSource = await readFile(new URL('../src/views/OverviewView.vue', import.meta.url), 'utf8')
const userPortalSource = await readFile(new URL('../src/views/UserPortalView.vue', import.meta.url), 'utf8')
const subscriberPortalSource = await readFile(new URL('../src/views/SubscriberPortalView.vue', import.meta.url), 'utf8')
const htmlSource = await readFile(new URL('../index.html', import.meta.url), 'utf8')

test('管理端、用户门户和订阅门户共用右上角账户菜单', () => {
  assert.match(appSource, /import AccountMenu from '.\/components\/AccountMenu\.vue'/)
  assert.match(appSource, /<AccountMenu v-if="state\.user" :user="state\.user" @updated="updateCurrentUser" @logout="logout"/)
  assert.match(userPortalSource, /<AccountMenu :user="props\.user" @updated="emit\('userUpdated', \$event\)" @logout="emit\('logout'\)"/)
  assert.match(subscriberPortalSource, /<AccountMenu :user="props\.user" @updated="emit\('userUpdated', \$event\)" @logout="emit\('logout'\)"/)
  assert.match(appSource, /function updateCurrentUser\(user: NonNullable<AuthState\['user'\]>\)[\s\S]*state\.value\.user = user/)
})

test('账户菜单显示当前用户名并提供改名、改密和退出', () => {
  for (const label of ['更改用户名', '更改密码', '退出登录', '新用户名', '当前密码', '确认新密码', '忘记当前密码？']) {
    assert.match(accountSource, new RegExp(label))
  }
  assert.match(accountSource, /label: props\.user\.username, key: 'username', disabled: true/)
  assert.match(accountSource, /\/api\/account\/username[\s\S]*method: 'PATCH'/)
  assert.match(accountSource, /\/api\/account\/password[\s\S]*method: 'POST'/)
  assert.match(accountSource, /密码修改成功，请重新登录。/)
  assert.match(accountSource, /\/api\/account\/password-reset-request[\s\S]*method: 'POST'/)
  assert.doesNotMatch(accountSource, /密码修改申请已提交/)
  assert.match(appSource, /忘记密码？/)
  assert.match(appSource, /\/api\/auth\/password-reset-request[\s\S]*method: 'POST'/)
  assert.match(appSource, /如果该账号存在，重置申请已提交，请等待管理员审核。/)
})

test('管理员密码重置申请列表显示角色、时间和待审核状态', () => {
  assert.match(overviewSource, /密码重置申请/)
  assert.match(overviewSource, /request\.username/)
  assert.match(overviewSource, /userRoleLabel\(request\.role\)/)
  assert.match(overviewSource, /formatTime\(request\.created_at\)/)
  assert.match(overviewSource, /<n-tag type="warning" size="small">待审核<\/n-tag>/)
  assert.match(overviewSource, /reviewPasswordChangeRequest\(request\.id, 'reject'\)/)
  assert.match(overviewSource, /reviewPasswordChangeRequest\(request\.id, 'approve'\)/)
})

test('所有可见产品品牌统一为夕凪云', () => {
  for (const source of [appSource, userPortalSource, subscriberPortalSource, htmlSource]) {
    assert.match(source, /夕凪云/)
    assert.doesNotMatch(source, />VPS Panel</)
  }
})
