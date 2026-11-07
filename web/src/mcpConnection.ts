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

// This repair path only updates the token variable of a matching existing
// Codex HTTP configuration. Registration stays in generateMCPConnection.
function windowsCodexRepair(endpoint: string, token?: string): string {
  return [
    '& {',
    "  if (-not (Get-Command codex -ErrorAction SilentlyContinue)) { throw '找不到 codex 命令。' }",
    '  $vpsPanelMcpJson = & codex mcp get vps-panel --json 2>$null',
    "  if ($LASTEXITCODE -ne 0) { throw '无法读取现有配置，请先确认它是否存在。' }",
    "  try { $vpsPanelMcpConfig = $vpsPanelMcpJson | ConvertFrom-Json -ErrorAction Stop } catch { throw '无法解析现有配置，未更改认证。' }",
    '  $vpsPanelMcpTransport = $vpsPanelMcpConfig.transport',
    `  if ($vpsPanelMcpTransport.type -cne 'streamable_http' -or $vpsPanelMcpTransport.url -cne ${quotePowerShell(endpoint)}) { throw '现有 MCP 地址或传输方式不匹配，请先确认差异；未更改配置。' }`,
    "  if ($vpsPanelMcpConfig.enabled -eq $false) { throw '现有 MCP 已禁用，请先确认如何处理。' }",
    '  foreach ($vpsPanelMcpHeaders in @($vpsPanelMcpTransport.http_headers, $vpsPanelMcpTransport.env_http_headers)) {',
    "    if ($null -ne $vpsPanelMcpHeaders -and @($vpsPanelMcpHeaders.PSObject.Properties.Name) -contains 'Authorization') { throw '现有认证方式存在冲突，请先确认。' }",
    '  }',
    "  if ($vpsPanelMcpTransport.http_headers_helper) { throw '现有认证使用请求头助手，请先确认。' }",
    '  $vpsPanelMcpEnvName = $vpsPanelMcpTransport.bearer_token_env_var',
    "  if ($vpsPanelMcpEnvName -isnot [string] -or $vpsPanelMcpEnvName -cnotmatch '^[A-Za-z_][A-Za-z0-9_]*$' -or $vpsPanelMcpEnvName -match '^(HOME|PATH|CODEX_HOME|USERPROFILE|SYSTEMROOT|COMSPEC|HOMEDRIVE|HOMEPATH|TEMP|TMP|PSMODULEPATH)$') { throw '现有认证变量不可安全复用，请先确认认证方式。' }",
    ...windowsToken(token),
    "  if ([string]::IsNullOrWhiteSpace($vpsPanelMcpToken)) { throw '令牌不能为空。' }",
    "  [Environment]::SetEnvironmentVariable($vpsPanelMcpEnvName, $vpsPanelMcpToken, 'User')",
    "  [Environment]::SetEnvironmentVariable($vpsPanelMcpEnvName, $vpsPanelMcpToken, 'Process')",
    "  Write-Host ('已保留现有 MCP 配置并更新认证变量 ' + $vpsPanelMcpEnvName + '。当前运行的 Codex 进程可能需要完全退出并重启，随后验证工具调用。')",
    '}',
  ].join('\n')
}

function unixCodexRepair(endpoint: string, token?: string): string {
  // Only the validated variable name is emitted; config headers stay in memory.
  const parser = [
    'import json, re, sys',
    'try:',
    '    c = json.load(sys.stdin)',
    '    t = c["transport"]',
    '    if c.get("enabled") is False or t.get("type") != "streamable_http" or t.get("url") != sys.argv[1]: raise ValueError()',
    '    for headers in (t.get("http_headers"), t.get("env_http_headers")):',
    '        if headers and (not isinstance(headers, dict) or any(str(k).lower() == "authorization" for k in headers)): raise ValueError()',
    '    if t.get("http_headers_helper"): raise ValueError()',
    '    name = t.get("bearer_token_env_var")',
    '    if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name) or name.upper() in ("HOME", "PATH", "CODEX_HOME", "USERPROFILE", "SYSTEMROOT", "COMSPEC", "HOMEDRIVE", "HOMEPATH", "TEMP", "TMP", "PSMODULEPATH"): raise ValueError()',
    '    print(name)',
    'except (ValueError, TypeError, KeyError, AttributeError):',
    '    print("现有 MCP 地址、传输或认证方式不匹配，请先确认；未更改配置。", file=sys.stderr)',
    '    sys.exit(1)',
  ].join('\n')
  const lines = [
    'vps_panel_mcp_repair() {',
    '  local vps_panel_mcp_json vps_panel_mcp_name vps_panel_mcp_token',
    "  if ! command -v codex >/dev/null 2>&1; then printf '%s\\n' '找不到 codex 命令。' >&2; return 1; fi",
    "  if ! command -v python3 >/dev/null 2>&1; then printf '%s\\n' '缺少 python3，请使用可用工具检查现有 JSON 配置后处理。' >&2; return 1; fi",
    "  if ! vps_panel_mcp_json=\"$(codex mcp get vps-panel --json 2>/dev/null)\"; then printf '%s\\n' '无法读取现有配置，请先确认它是否存在。' >&2; return 1; fi",
    `  vps_panel_mcp_name="$(printf '%s' "$vps_panel_mcp_json" | python3 -c ${quoteBash(parser)} ${quoteBash(endpoint)})" || return 1`,
  ]
  if (token !== undefined) lines.push(`  vps_panel_mcp_token=${quoteBash(token)}`)
  else lines.push("  printf '%s' '请输入 MCP 令牌: '", '  IFS= read -r -s vps_panel_mcp_token', "  printf '\\n'")
  lines.push(
    "  if [ -z \"$vps_panel_mcp_token\" ]; then printf '%s\\n' '令牌不能为空。' >&2; return 1; fi",
    '  export "$vps_panel_mcp_name=$vps_panel_mcp_token"',
    "  printf '%s\\n' '已保留现有 MCP 配置并在当前终端更新认证变量。桌面应用不一定继承该变量；请在确认启动环境后安全配置并重新验证。'",
    '}',
    'if vps_panel_mcp_repair; then',
    '  unset -f vps_panel_mcp_repair',
    'else',
    '  unset -f vps_panel_mcp_repair',
    '  false',
    'fi',
  )
  return lines.join('\n')
}

export function generateMCPAgentPrompt(client: MCPClient, platform: MCPPlatform, endpoint: string, token?: string): string {
  const config = generateMCPConnection(client, platform, endpoint, token)
  const repair = client === 'codex' ? (platform === 'windows' ? windowsCodexRepair(endpoint, token) : unixCodexRepair(endpoint, token)) : ''
  // Pick a fence longer than any backticks in the generated configuration.
  const longestRun = Math.max(0, ...((config + repair).match(/`+/g) ?? []).map(run => run.length))
  const fence = '`'.repeat(Math.max(3, longestRun + 1))
  const clientName = client === 'codex' ? 'Codex Desktop / CLI' : client === 'claude' ? 'Claude Code' : 'Cursor'
  const location = client === 'cursor'
    ? `将下面的 JSON 中 vps-panel 条目合并到 Cursor 的 ~/.cursor/mcp.json 或项目的 .cursor/mcp.json 的 mcpServers 中。保留其他已有 MCP 配置，不能用此 JSON 替换整个配置文件。${token === undefined ? '请确保 Cursor 进程能够读取 VPS_PANEL_MCP_TOKEN 环境变量。' : '该 JSON 会将令牌保存到配置文件，请提醒我保护该文件。'}`
    : `在当前 ${platform === 'windows' ? 'Windows PowerShell' : 'macOS / Linux Bash'} 环境中按以下情况操作，也可以使用 ${clientName} 支持的等效配置方式。`
  const tokenInstructions = token === undefined
    ? '此配置不包含令牌明文。请引导我在受信任的终端中输入令牌；如果无法交互输入，请说明如何安全设置所需环境变量。不要擅自查找或读取其他应用的敏感凭据。'
    : '我已授权你使用提供的令牌完成此 MCP 安装，必要时可在工具参数中使用。不要在普通回复或公开日志中打印令牌，不要提交到 Git、写入项目源码或发送给其他服务。令牌可能出现在本次 AI 对话及工具执行记录中，请如实告知。'
  const clientVerification = client === 'claude'
    ? 'Claude Code 会将 Bearer 请求头保存到用户配置文件，请提醒我保护该文件。可使用 claude mcp get vps-panel 和 /mcp 检查配置与连接。'
    : client === 'cursor'
      ? '检查 Cursor 的 MCP 设置与工具发现；如果无法编辑配置，请给我保留其他配置的手动合并步骤。'
      : '使用 Codex 支持的方式检查注册配置；如果需要，提醒我完全退出并重启 Codex Desktop。'
  const language = client === 'cursor' ? 'json' : platform === 'windows' ? 'powershell' : 'bash'

  return [
    `请在当前环境及可用工具权限允许的范围内，帮我为 ${clientName} 配置 VPS Panel MCP，并验证连接。`,
    `MCP 名称：vps-panel\nMCP 地址：${endpoint}\n传输：Streamable HTTP\n鉴权：Bearer Token`,
    client === 'codex'
      ? '先用 codex mcp get vps-panel --json 检查配置，避免输出可能包含凭据的完整 JSON；读取 transport.type、transport.url 和 transport.bearer_token_env_var。若地址和传输正确且为 Bearer 环境变量认证，保留配置，按实际变量名补齐令牌，不要删除或重新注册；例如已有 VPS_PANEL_TOKEN 时继续使用它。若不存在，使用下方首次注册配置及默认变量 VPS_PANEL_MCP_TOKEN；不能仅凭查询失败断定不存在，应先检查 MCP 列表。地址或认证方式冲突时说明差异并先征求我确认，不得静默覆盖。'
      : '请先检查当前 MCP 配置。如果已存在同名 vps-panel，先检查其内容；未经我确认，不要删除或覆盖。',
    location,
    tokenInstructions,
    ...(client === 'codex' ? [`已有配置且地址、传输和认证匹配时，用以下代码复用实际变量；否则停止并说明。${platform === 'unix' ? '如缺少 python3，请用其他可用工具检查 JSON；export 仅影响当前终端，需根据桌面应用的实际启动环境安全处理持久化，不要把令牌写入 shell 历史或不受保护的文件。' : ''}\n${fence}${language}\n${repair}\n${fence}`] : []),
    `${client === 'codex' ? '仅在确认没有同名配置时使用以下首次注册配置' : '配置内容'}：\n${fence}${language}\n${config}\n${fence}`,
    clientVerification,
    `检查配置、地址和认证变量是否可被 ${clientName} 进程读取，只报告变量是否存在，不打印变量值；再发现工具列表并调用 get_panel_info 和 list_servers。只执行只读检查，不要伪造结果或修改 VPS Panel 业务资源。只有实际调用成功才能报告连接成功；如果当前会话无法验证，请标记“已注册，但尚未完成连接验证”，并说明具体阻塞、是否需要重启或手动操作。`,
  ].join('\n\n')
}
