import assert from 'node:assert/strict'
import { test } from 'node:test'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { generateMCPConnection, quoteBash, quotePowerShell } from '../src/mcpConnection.ts'

const endpoint = "https://example.test/a'$(printf INJECTED)/mcp"
const token = "token'\"$() ` backtick\nnext"
const bash = process.platform === 'win32' ? 'C:\\Program Files\\Git\\bin\\bash.exe' : '/bin/bash'
for (const platform of ['windows', 'unix']) {
  for (const client of ['codex', 'claude']) {
    test(`${client} ${platform}: 默认交互输入并可生成带令牌版本`, () => {
      const command = generateMCPConnection(client, platform, endpoint)
      const withToken = generateMCPConnection(client, platform, endpoint, token)
      assert.ok(!command.includes(token))
      assert.ok(withToken.includes(platform === 'windows' ? quotePowerShell(token) : quoteBash(token)))
      assert.ok(command.includes(platform === 'windows' ? quotePowerShell(endpoint) : quoteBash(endpoint)))
      assert.match(command, client === 'codex' ? /--bearer-token-env-var VPS_PANEL_MCP_TOKEN/ : /claude mcp add --transport http vps-panel --scope user/)
      assert.match(command, platform === 'windows' ? /Read-Host '请输入 MCP 令牌' -AsSecureString/ : /read -r -s vps_panel_mcp_token/)
    })
  }
}
test('Windows Codex 持久化用户变量、提供当前进程变量并确认替换', () => {
  const script = generateMCPConnection('codex', 'windows', endpoint)
  assert.match(script, /Get-Command codex/)
  assert.match(script, /SetEnvironmentVariable\('VPS_PANEL_MCP_TOKEN', \$vpsPanelMcpToken, 'User'\)/)
  assert.match(script, /\$env:VPS_PANEL_MCP_TOKEN = \$vpsPanelMcpToken/)
  assert.match(script, /输入 REPLACE 替换/)
  assert.ok(script.indexOf("Read-Host '已存在") < script.indexOf('codex mcp remove'))
  assert.match(script, /重启 Codex Desktop/)
})
test('Cursor JSON 使用环境变量插值，带令牌模式正确 JSON 转义', () => {
  const without = JSON.parse(generateMCPConnection('cursor', 'windows', endpoint))
  const withToken = JSON.parse(generateMCPConnection('cursor', 'unix', endpoint, token))
  assert.equal(without.mcpServers['vps-panel'].url, endpoint)
  assert.equal(without.mcpServers['vps-panel'].headers.Authorization, 'Bearer ${env:VPS_PANEL_MCP_TOKEN}')
  assert.equal(withToken.mcpServers['vps-panel'].headers.Authorization, `Bearer ${token}`)
})
test('拒绝 shell 无法表示的空字符', () => {
  assert.throws(() => quotePowerShell('x\0y'))
  assert.throws(() => quoteBash('x\0y'))
})
test('PowerShell 实际解析单引号、换行和特殊字符', { skip: process.platform !== 'win32' }, () => {
  const script = `$value = ${quotePowerShell(token)}; [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($value))`
  const result = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', script], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(Buffer.from(result.stdout.trim(), 'base64').toString('utf8'), token)
})
test('生成的 PowerShell 脚本均可被实际解析器解析', { skip: process.platform !== 'win32' }, () => {
  const check = '$text = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())); $tokens = $null; $errors = $null; [System.Management.Automation.Language.Parser]::ParseInput($text, [ref]$tokens, [ref]$errors) | Out-Null; if ($errors.Count -gt 0) { $errors | ForEach-Object { [Console]::Error.WriteLine($_.Message) }; exit 1 }'
  for (const client of ['codex', 'claude']) {
    for (const value of [undefined, token]) {
      const result = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', check], { input: Buffer.from(generateMCPConnection(client, 'windows', endpoint, value)).toString('base64'), encoding: 'utf8' })
      assert.equal(result.status, 0, `${client} ${value === undefined ? 'prompt' : 'embedded'}: ${result.stderr}`)
    }
  }
})
test('Bash 实际执行保留变量、正确转义且不执行注入', { skip: !existsSync(bash) }, () => {
  const script = [
    'codex() { if [ "$2" = get ]; then return 1; fi; printf "ARG:<%s>\\n" "$@"; }',
    generateMCPConnection('codex', 'unix', endpoint, token),
    'printf "TOKEN:<%s>\\n" "$VPS_PANEL_MCP_TOKEN"',
  ].join('\n')
  const result = spawnSync(bash, ['-c', script], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  assert.ok(result.stdout.includes(`ARG:<${endpoint}>`))
  assert.ok(result.stdout.includes(`TOKEN:<${token}>`))
  assert.ok(result.stdout.includes('ARG:<VPS_PANEL_MCP_TOKEN>'))
  assert.ok(!result.stdout.includes('ARG:<https://example.test/aINJECTED/mcp>'))
})
test('Bash 遇到已有 Codex 配置时不确认则不删除', { skip: !existsSync(bash) }, () => {
  const script = [
    'codex() { if [ "$2" = get ]; then return 0; fi; printf "CALLED:%s\\n" "$2"; }',
    generateMCPConnection('codex', 'unix', endpoint, token),
  ].join('\n')
  const result = spawnSync(bash, ['-c', script], { input: 'NO\n', encoding: 'utf8' })
  assert.equal(result.status, 1, result.stderr)
  assert.match(result.stdout, /原配置未更改/)
  assert.doesNotMatch(result.stdout, /CALLED:(remove|add)/)
})
test('Bash 确认替换才移除旧配置并注册，移除失败时不注册', { skip: !existsSync(bash) }, () => {
  for (const removeExit of [0, 1]) {
    const script = [
      `codex() { if [ "$2" = get ]; then return 0; fi; printf "CALLED:%s\\n" "$2"; if [ "$2" = remove ]; then return ${removeExit}; fi; }`,
      generateMCPConnection('codex', 'unix', endpoint, token),
    ].join('\n')
    const result = spawnSync(bash, ['-c', script], { input: 'REPLACE\n', encoding: 'utf8' })
    assert.equal(result.status, removeExit)
    assert.match(result.stdout, /CALLED:remove/)
    if (removeExit === 0) assert.match(result.stdout, /CALLED:add/)
    else { assert.doesNotMatch(result.stdout, /CALLED:add/); assert.match(result.stderr, /移除原配置失败/) }
  }
})
