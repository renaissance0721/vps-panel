export type MCPClient = 'codex' | 'claude' | 'cursor'
export type MCPPlatform = 'windows' | 'unix'

// Single-quoted literals do not interpolate shell metacharacters.
export function quotePowerShell(value: string): string {
  if (value.includes('\0')) throw new Error('配置内容不能包含空字符')
  return `'${value.replaceAll("'", "''")}'`
}

export function quoteBash(value: string): string {
  if (value.includes('\0')) throw new Error('配置内容不能包含空字符')
  return `'${value.replaceAll("'", `'"'"'`)}'`
}

function windowsToken(token?: string): string[] {
  if (token !== undefined) return [`  $vpsPanelMcpToken = ${quotePowerShell(token)}`]
  return [
    "  $vpsPanelMcpSecureToken = Read-Host '请输入 MCP 令牌' -AsSecureString",
    '  $vpsPanelMcpPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($vpsPanelMcpSecureToken)',
    '  try { $vpsPanelMcpToken = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($vpsPanelMcpPointer) }',
    '  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($vpsPanelMcpPointer) }',
  ]
}

function windowsCommand(client: 'codex' | 'claude', endpoint: string, token?: string): string {
  const command = client === 'codex' ? 'codex' : 'claude'
  const lines = [
    '& {',
    `  if (-not (Get-Command ${command} -ErrorAction SilentlyContinue)) { Write-Error '找不到 ${command} 命令，请先安装并重启 PowerShell。'; return }`,
    ...(client === 'codex' ? ["  $vpsPanelMcpReplace = ''"] : []),
    `  & ${command} mcp get vps-panel *> $null`,
    '  if ($LASTEXITCODE -eq 0) {',
  ]
  if (client === 'codex') {
    lines.push(
      "    $vpsPanelMcpReplace = Read-Host '已存在 vps-panel 配置。输入 REPLACE 替换，否则取消'",
      "    if ($vpsPanelMcpReplace -cne 'REPLACE') { Write-Host '已取消，原配置未更改。'; return }",
    )
  } else {
    lines.push("    Write-Error '已存在 vps-panel 配置，请检查其作用域并手动移除后重试。'; return")
  }
  lines.push('  }', ...windowsToken(token),
    "  if ([string]::IsNullOrWhiteSpace($vpsPanelMcpToken)) { Write-Error '令牌不能为空。'; return }")
  if (client === 'codex') {
    lines.push(
      "  [Environment]::SetEnvironmentVariable('VPS_PANEL_MCP_TOKEN', $vpsPanelMcpToken, 'User')",
      '  $env:VPS_PANEL_MCP_TOKEN = $vpsPanelMcpToken',
      "  if ($vpsPanelMcpReplace -ceq 'REPLACE') {",
      '    & codex mcp remove vps-panel',
      "    if ($LASTEXITCODE -ne 0) { Write-Error '移除原配置失败，未注册新配置。'; return }",
      '  }',
      `  & codex mcp add vps-panel --url ${quotePowerShell(endpoint)} --bearer-token-env-var VPS_PANEL_MCP_TOKEN`,
      "  if ($LASTEXITCODE -ne 0) { Write-Error '注册失败；若刚替换旧配置，请检查并恢复配置。'; return }",
      "  Write-Host '配置已保存。请完全退出并重启 Codex Desktop，再检查 MCP 连接。'",
    )
  } else {
    lines.push(
      `  & claude mcp add --transport http vps-panel --scope user ${quotePowerShell(endpoint)} --header ("Authorization: Bearer " + $vpsPanelMcpToken)`,
      "  if ($LASTEXITCODE -ne 0) { Write-Error '注册失败，请检查 Claude Code 的输出。'; return }",
      "  Write-Host '配置已保存。运行 claude mcp get vps-panel 或在 Claude Code 中使用 /mcp 检查连接。'",
    )
  }
  lines.push('}')
  return lines.join('\n')
}

function unixCommand(client: 'codex' | 'claude', endpoint: string, token?: string): string {
  const command = client === 'codex' ? 'codex' : 'claude'
  const lines = [
    'vps_panel_mcp_setup() {',
    '  local vps_panel_mcp_token vps_panel_mcp_replace',
    `  if ! command -v ${command} >/dev/null 2>&1; then printf '%s\\n' '找不到 ${command} 命令，请先安装。' >&2; return 1; fi`,
    `  if ${command} mcp get vps-panel >/dev/null 2>&1; then`,
  ]
  if (client === 'codex') {
    lines.push(
      "    printf '%s' '已存在 vps-panel 配置。输入 REPLACE 替换，否则取消: '",
      '    IFS= read -r vps_panel_mcp_replace',
      "    if [ \"$vps_panel_mcp_replace\" != 'REPLACE' ]; then printf '%s\\n' '已取消，原配置未更改。'; return 1; fi",
    )
  } else {
    lines.push("    printf '%s\\n' '已存在 vps-panel 配置，请检查其作用域并手动移除后重试。' >&2; return 1")
  }
  lines.push('  fi')
  if (token !== undefined) lines.push(`  vps_panel_mcp_token=${quoteBash(token)}`)
  else lines.push("  printf '%s' '请输入 MCP 令牌: '", '  IFS= read -r -s vps_panel_mcp_token', "  printf '\\n'")
  lines.push("  if [ -z \"$vps_panel_mcp_token\" ]; then printf '%s\\n' '令牌不能为空。' >&2; return 1; fi")
  if (client === 'codex') {
    lines.push(
      '  export VPS_PANEL_MCP_TOKEN="$vps_panel_mcp_token"',
      '  if [ "${vps_panel_mcp_replace:-}" = REPLACE ]; then',
      "    codex mcp remove vps-panel || { printf '%s\\n' '移除原配置失败，未注册新配置。' >&2; return 1; }",
      '  fi',
      `  codex mcp add vps-panel --url ${quoteBash(endpoint)} --bearer-token-env-var VPS_PANEL_MCP_TOKEN || { printf '%s\\n' '注册失败；若刚替换旧配置，请检查并恢复配置。' >&2; return 1; }`,
      "  printf '%s\\n' '配置已保存。请从具有该环境变量的环境启动 Codex；桌面应用需完全退出并重启。'",
    )
  } else {
    lines.push(
      `  claude mcp add --transport http vps-panel --scope user ${quoteBash(endpoint)} --header "Authorization: Bearer $vps_panel_mcp_token" || { printf '%s\\n' '注册失败，请检查 Claude Code 的输出。' >&2; return 1; }`,
      "  printf '%s\\n' '配置已保存。运行 claude mcp get vps-panel 或在 Claude Code 中使用 /mcp 检查连接。'",
    )
  }
  lines.push('}', 'if vps_panel_mcp_setup; then', '  unset -f vps_panel_mcp_setup', 'else', '  unset -f vps_panel_mcp_setup', '  false', 'fi')
  return lines.join('\n')
}

export function generateMCPConnection(client: MCPClient, platform: MCPPlatform, endpoint: string, token?: string): string {
  if (client === 'cursor') {
    return JSON.stringify({ mcpServers: { 'vps-panel': { url: endpoint, headers: { Authorization: `Bearer ${token === undefined ? '${env:VPS_PANEL_MCP_TOKEN}' : token}` } } } }, null, 2)
  }
  return platform === 'windows' ? windowsCommand(client, endpoint, token) : unixCommand(client, endpoint, token)
}

export function generateMCPAgentPrompt(client: MCPClient, platform: MCPPlatform, endpoint: string, token?: string): string {
  const config = generateMCPConnection(client, platform, endpoint, token)
  // Pick a fence longer than any backticks in the generated configuration.
  const longestRun = Math.max(0, ...(config.match(/`+/g) ?? []).map(run => run.length))
  const fence = '`'.repeat(Math.max(3, longestRun + 1))
  const clientName = client === 'codex' ? 'Codex Desktop / CLI' : client === 'claude' ? 'Claude Code' : 'Cursor'
  const location = client === 'cursor'
    ? `将下面的 JSON 中 vps-panel 条目合并到 Cursor 的 ~/.cursor/mcp.json 或项目的 .cursor/mcp.json 的 mcpServers 中。保留其他已有 MCP 配置，不能用此 JSON 替换整个配置文件。${token === undefined ? '请确保 Cursor 进程能够读取 VPS_PANEL_MCP_TOKEN 环境变量。' : '该 JSON 会将令牌保存到配置文件，请提醒我保护该文件。'}`
    : `在当前 ${platform === 'windows' ? 'Windows PowerShell' : 'macOS / Linux Bash'} 环境中，使用下面的配置命令；也可以使用 ${clientName} 支持的等效配置方式。`
  const tokenInstructions = token === undefined
    ? '此配置不包含令牌明文。请引导我在受信任的终端中输入令牌；如果无法交互输入，请说明如何安全设置所需环境变量。不要擅自查找或读取其他应用的敏感凭据。'
    : '下面的配置包含完整令牌。请仅用于此 MCP 鉴权，不要输出、记录或发送给其他服务；避免将含令牌的命令写入终端历史或日志。'
  const clientVerification = client === 'claude'
    ? 'Claude Code 会将 Bearer 请求头保存到用户配置文件，请提醒我保护该文件。可使用 claude mcp get vps-panel 和 /mcp 检查配置与连接。'
    : client === 'cursor'
      ? '检查 Cursor 的 MCP 设置与工具发现；如果无法编辑配置，请给我保留其他配置的手动合并步骤。'
      : '使用 Codex 支持的方式检查注册配置；如果需要，提醒我完全退出并重启 Codex Desktop。'
  const language = client === 'cursor' ? 'json' : platform === 'windows' ? 'powershell' : 'bash'

  return [
    `请在当前环境及可用工具权限允许的范围内，帮我为 ${clientName} 配置 VPS Panel MCP，并验证连接。`,
    `MCP 名称：vps-panel\nMCP 地址：${endpoint}\n传输：Streamable HTTP\n鉴权：Bearer Token`,
    '请先检查当前 MCP 配置。如果已存在同名 vps-panel，先检查其内容；未经我确认，不要删除或覆盖。',
    location,
    tokenInstructions,
    `配置内容：\n${fence}${language}\n${config}\n${fence}`,
    clientVerification,
    '验证时先检查配置和认证，尝试发现工具列表（tools/list），再实际调用 get_panel_info 和 list_servers。只执行这些只读检查，不要修改任何 VPS Panel 业务资源，也不要伪造工具结果。',
    '仅有配置注册成功不代表连接成功。请分别报告注册、认证、工具发现和实际调用的结果；只有验证通过才能报告端到端连接成功。如果当前会话无法发现或调用新 MCP，请标记“已注册，但尚未完成连接验证”，并说明是否需要重启客户端。',
    '如果缺少所需终端、客户端命令、执行权限或配置访问权限，请说明具体阻塞和需要我手动执行的步骤，不要声称安装成功。不要输出或记录令牌明文。',
  ].join('\n\n')
}
