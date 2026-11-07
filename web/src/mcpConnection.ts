export type MCPConnectionClient = 'codex' | 'claude-code' | 'cursor'
export type MCPConnectionPlatform = 'windows' | 'unix'

export const mcpConnectionClients: Array<{ label: string; value: MCPConnectionClient }> = [
  { label: 'Codex Desktop / CLI', value: 'codex' },
  { label: 'Claude Code', value: 'claude-code' },
  { label: 'Cursor', value: 'cursor' },
]

export const mcpConnectionPlatforms: Array<{ label: string; value: MCPConnectionPlatform }> = [
  { label: 'Windows PowerShell', value: 'windows' },
  { label: 'macOS / Linux 终端', value: 'unix' },
]

const psQuote = (value: string) => "'" + value.replaceAll("'", "''") + "'"
const shQuote = (value: string) => "'" + value.replaceAll("'", "'\\''") + "'"

/**
 * Generate a copyable client configuration without storing a token anywhere.
 * Passing token embeds it in the resulting text: use only while the one-time
 * token reveal is open, and warn about clipboard / shell-history exposure.
 */
export function createMCPConnectionSnippet(
  client: MCPConnectionClient,
  platform: MCPConnectionPlatform,
  endpoint: string,
  token = '',
): string {
  if (client === 'codex') {
    if (platform === 'windows') {
      const acquireToken = token
        ? '$token = ' + psQuote(token)
        : [
            '$secureToken = Read-Host "粘贴 MCP Token" -AsSecureString',
            '$token = [System.Net.NetworkCredential]::new("", $secureToken).Password',
            'Remove-Variable secureToken',
          ].join('\n')
      return [
        '# 在 Windows PowerShell 执行，完成后重启 Codex Desktop',
        'if (-not (Get-Command codex -ErrorAction SilentlyContinue)) { throw "未找到 Codex CLI，请先安装 Codex CLI 或手动配置 ~/.codex/config.toml" }',
        acquireToken,
        '[Environment]::SetEnvironmentVariable("VPS_PANEL_MCP_TOKEN", $token, "User")',
        '$env:VPS_PANEL_MCP_TOKEN = $token',
        'Remove-Variable token',
        '# 替换同名的旧配置（如果存在）',
        'codex mcp remove vps-panel 2>$null',
        'codex mcp add vps-panel --url ' + psQuote(endpoint) + ' --bearer-token-env-var VPS_PANEL_MCP_TOKEN',
      ].join('\n')
    }
    const acquireToken = token
      ? 'export VPS_PANEL_MCP_TOKEN=' + shQuote(token)
      : 'read -r -s -p "粘贴 MCP Token: " VPS_PANEL_MCP_TOKEN; echo; export VPS_PANEL_MCP_TOKEN'
    return [
      '# macOS / Linux：终端会话配置；Codex Desktop 还需要能读取该环境变量',
      acquireToken,
      'codex mcp remove vps-panel 2>/dev/null || true',
      'codex mcp add vps-panel --url ' + shQuote(endpoint) + ' --bearer-token-env-var VPS_PANEL_MCP_TOKEN',
      '# 注意：export 仅对当前终端及子进程有效，不能保证桌面应用读取到 Token',
    ].join('\n')
  }

  if (client === 'claude-code') {
    const bearer = token || '<粘贴 MCP Token>'
    if (platform === 'windows') {
      return 'claude mcp add --transport http vps-panel ' + psQuote(endpoint) +
        ' --header ' + psQuote('Authorization: Bearer ' + bearer)
    }
    return 'claude mcp add --transport http vps-panel ' + shQuote(endpoint) +
      ' --header ' + shQuote('Authorization: Bearer ' + bearer)
  }

  return JSON.stringify({
    mcpServers: {
      'vps-panel': {
        url: endpoint,
        headers: { Authorization: 'Bearer ' + (token || '<粘贴 MCP Token>') },
      },
    },
  }, null, 2)
}
