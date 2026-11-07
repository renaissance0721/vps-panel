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
