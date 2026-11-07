import assert from 'node:assert/strict'
import { test } from 'node:test'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { generateMCPAgentPrompt, generateMCPConnection, quoteBash, quotePowerShell } from '../src/mcpConnection.ts'

const endpoint = "https://example.test/a'$(printf INJECTED)/mcp"
const token = "token'\"$() ` backtick\nnext"
const bash = process.platform === 'win32' ? 'C:\\Program Files\\Git\\bin\\bash.exe' : '/bin/bash'

function codexRepair(platform, value = token) {
  const prompt = generateMCPAgentPrompt('codex', platform, endpoint, value)
  const language = platform === 'windows' ? 'powershell' : 'bash'
  const match = prompt.match(new RegExp(`(\u0060{3,})${language}\\n([\\s\\S]*?)\\n\\1`))
  assert.ok(match, 'missing repair snippet')
  return match[2]
}

function unfilledCodexRepair(platform) {
  const prompt = generateMCPAgentPrompt('codex', platform, endpoint)
  const language = platform === 'windows' ? 'powershell' : 'bash'
  return prompt.match(new RegExp(`(\u0060{3,})${language}\\n([\\s\\S]*?)\\n\\1`))[2]
}

function existingConfig(overrides = {}) {
  return { enabled: true, transport: { type: 'streamable_http', url: endpoint, bearer_token_env_var: 'VPS_PANEL_TOKEN', http_headers: null, env_http_headers: null, http_headers_helper: null, ...overrides } }
}

function runWindowsRepair(config) {
  // Replace the real user environment writer with a child-process-only test double.
  const repair = codexRepair('windows').replaceAll('[Environment]::SetEnvironmentVariable(', '[McpTestEnvironment]::SetEnvironmentVariable(')
  const script = [
    `Add-Type -TypeDefinition 'public static class McpTestEnvironment { public static void SetEnvironmentVariable(string n, string v, string s) { System.Environment.SetEnvironmentVariable("VPS_PANEL_TEST_" + s, n); System.Environment.SetEnvironmentVariable("VPS_PANEL_TEST_VALUE", v); } }'`,
    '$env:VPS_PANEL_TEST_User = "none"; $env:VPS_PANEL_TEST_Process = "none"; $env:VPS_PANEL_TEST_VALUE = "none"',
    `$vpsPanelTestJson = ${quotePowerShell(JSON.stringify(config))}`,
    "function codex { $global:LASTEXITCODE = 0; if ($args[1] -eq 'get') { Write-Output $vpsPanelTestJson } else { Write-Output ('UNEXPECTED:' + $args[1]) } }",
    `try {\n${repair}\n} catch { [Console]::Error.WriteLine($_.Exception.Message) }`,
    "[Console]::WriteLine('USER=' + $env:VPS_PANEL_TEST_User)",
    "[Console]::WriteLine('PROCESS=' + $env:VPS_PANEL_TEST_Process)",
    "[Console]::WriteLine('TOKEN=' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($env:VPS_PANEL_TEST_VALUE)))",
  ].join('\n')
  return spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')], { encoding: 'utf8' })
}

test('Windows 首次注册使用默认变量且不修改项目文件', { skip: process.platform !== 'win32' }, () => {
  const original = generateMCPConnection('codex', 'windows', endpoint, token)
  assert.doesNotMatch(original, /Set-Content|Add-Content|Out-File/)
  const safe = original.replace("[Environment]::SetEnvironmentVariable('VPS_PANEL_MCP_TOKEN', $vpsPanelMcpToken, 'User')", "$global:vpsPanelTestSaved = $vpsPanelMcpToken")
  const script = [
    '$global:vpsPanelTestSaved = "none"; $global:vpsPanelTestCalls = @()',
    "function codex { if ($args[1] -eq 'get') { $global:LASTEXITCODE = 1 } else { $global:vpsPanelTestCalls += ($args -join '|'); $global:LASTEXITCODE = 0 } }",
    safe,
    "[Console]::WriteLine('CALLS=' + ($global:vpsPanelTestCalls -join ','))",
    "[Console]::WriteLine('TOKEN=' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($env:VPS_PANEL_MCP_TOKEN)))",
    "[Console]::WriteLine('USER=' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($global:vpsPanelTestSaved)))",
  ].join('\n')
  const result = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  assert.ok(result.stdout.includes(`CALLS=mcp|add|vps-panel|--url|${endpoint}|--bearer-token-env-var|VPS_PANEL_MCP_TOKEN`))
  assert.ok(result.stdout.includes(`TOKEN=${Buffer.from(token).toString('base64')}`))
  assert.ok(result.stdout.includes(`USER=${Buffer.from(token).toString('base64')}`))
})

test('Windows 复用旧认证变量，且不删除或重新注册配置', { skip: process.platform !== 'win32' }, () => {
  const result = runWindowsRepair(existingConfig())
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.stdout, /USER=VPS_PANEL_TOKEN/)
  assert.match(result.stdout, /PROCESS=VPS_PANEL_TOKEN/)
  assert.ok(result.stdout.includes(`TOKEN=${Buffer.from(token).toString('base64')}`))
  assert.doesNotMatch(result.stdout, /UNEXPECTED:(add|remove)/)
})

test('Windows 地址或认证冲突时停止且不写入环境变量', { skip: process.platform !== 'win32' }, () => {
  for (const config of [existingConfig({ url: 'https://other.test/mcp' }), existingConfig({ type: 'stdio' }), existingConfig({ bearer_token_env_var: null }), existingConfig({ http_headers: { Authorization: 'placeholder' } }), existingConfig({ bearer_token_env_var: 'PATH' })]) {
    const result = runWindowsRepair(config)
    assert.equal(result.status, 0)
    assert.match(result.stdout, /USER=none/)
    assert.match(result.stdout, /PROCESS=none/)
    assert.doesNotMatch(result.stdout, /UNEXPECTED:(add|remove)/)
    assert.match(result.stderr, /未更改配置|请先确认|不可安全复用/)
  }
})

test('Bash 复用旧变量；地址与认证冲突时不更改配置', { skip: !existsSync(bash) }, t => {
  const pythonAvailable = spawnSync(bash, ['-c', 'python3 -c "import json"'], { encoding: 'utf8' }).status === 0
  if (!pythonAvailable) { t.skip('python3 不可用'); return }
  for (const [config, expected] of [[existingConfig(), token], [existingConfig({ url: 'https://other.test/mcp' }), 'old'], [existingConfig({ bearer_token_env_var: null }), 'old'], [existingConfig({ env_http_headers: { Authorization: 'OTHER_TOKEN' } }), 'old']]) {
    const script = [
      `vps_panel_test_config=${quoteBash(JSON.stringify(config))}`,
      "codex() { if [ \"$2\" = get ]; then printf '%s' \"$vps_panel_test_config\"; else printf 'UNEXPECTED:%s\\n' \"$2\"; fi; }",
      'export VPS_PANEL_TOKEN=old',
      codexRepair('unix'),
      'printf "VALUE:<%s>\\n" "$VPS_PANEL_TOKEN"',
    ].join('\n')
    const result = spawnSync(bash, ['-c', script], { encoding: 'utf8' })
    assert.equal(result.status, 0, result.stderr)
    assert.ok(result.stdout.includes(`VALUE:<${expected}>`))
    assert.doesNotMatch(result.stdout, /UNEXPECTED:(add|remove)/)
  }
})

test('Codex Windows 提示词复用原始命令并要求真实只读验证', () => {
  const before = generateMCPConnection('codex', 'windows', endpoint)
  const prompt = generateMCPAgentPrompt('codex', 'windows', endpoint)
  assert.ok(prompt.includes('Codex Desktop / CLI'))
  assert.ok(prompt.includes(endpoint))
  assert.match(prompt, /Streamable HTTP/)
  assert.ok(prompt.includes(before))
  assert.match(prompt, /get_panel_info/)
  assert.match(prompt, /list_servers/)
  assert.doesNotMatch(prompt, /get_overview/)
  assert.doesNotMatch(prompt, /one-time-secret/)
  assert.match(prompt, /保留配置，按实际变量名补齐令牌/)
  assert.match(prompt, /已有 VPS_PANEL_TOKEN 时继续使用它/)
  assert.match(prompt, /地址或认证方式冲突时说明差异并先征求我确认/)
  assert.match(prompt, /已注册，但尚未完成连接验证/)
  assert.match(prompt, /不要伪造结果/)
  assert.match(prompt, /不等于认证或连接成功|只有实际调用成功才能报告连接成功/)
  assert.equal(generateMCPConnection('codex', 'windows', endpoint), before)
})

test('含特殊字符的令牌仅在显式传入时嵌入配置，代码围栏保持完整', () => {
  const trickyToken = `${token}\n\`\`\`\`\`\nend`
  for (const client of ['codex', 'claude', 'cursor']) {
    for (const platform of ['windows', 'unix']) {
      const raw = generateMCPConnection(client, platform, endpoint, trickyToken)
      const prompt = generateMCPAgentPrompt(client, platform, endpoint, trickyToken)
      assert.ok(prompt.includes(raw))
      assert.ok(!generateMCPAgentPrompt(client, platform, endpoint).includes(trickyToken))
      const fence = prompt.match(/(?:配置内容|仅在确认没有同名配置时使用以下首次注册配置)：\n(`+)(?:powershell|bash|json)\n/)[1]
      assert.ok(fence.length > Math.max(0, ...(raw.match(/`+/g) ?? []).map(value => value.length)))
      assert.ok(prompt.includes(`\n${raw}\n${fence}`))
    }
  }
})
test('含令牌提示词授权必要工具使用且说明记录风险', () => {
  const prompt = generateMCPAgentPrompt('codex', 'windows', endpoint, token)
  assert.match(prompt, /已授权你使用提供的令牌/)
  assert.match(prompt, /必要时可在工具参数中使用/)
  assert.match(prompt, /AI 对话及工具执行记录/)
  assert.match(prompt, /不要在普通回复或公开日志中打印令牌/)
  assert.doesNotMatch(prompt, /不要输出、记录或发送给其他服务/)
})

test('Claude 和 Cursor 的提示词分别说明验证与合并配置', () => {
  const claude = generateMCPAgentPrompt('claude', 'unix', endpoint)
  assert.match(claude, /Claude Code/)
  assert.match(claude, /macOS \/ Linux Bash/)
  assert.match(claude, /claude mcp get vps-panel/)
  assert.match(claude, /\/mcp/)
  const cursor = generateMCPAgentPrompt('cursor', 'windows', endpoint)
  assert.match(cursor, /Cursor/)
  assert.match(cursor, /合并到 Cursor/)
  assert.match(cursor, /保留其他已有 MCP 配置/)
  assert.match(cursor, /手动合并步骤/)
  assert.ok(cursor.includes(generateMCPConnection('cursor', 'windows', endpoint)))
  assert.doesNotMatch(cursor, /get_overview/)
})
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
  for (const snippet of [unfilledCodexRepair('windows'), codexRepair('windows')]) {
    const result = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', check], { input: Buffer.from(snippet).toString('base64'), encoding: 'utf8' })
    assert.equal(result.status, 0, result.stderr)
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
