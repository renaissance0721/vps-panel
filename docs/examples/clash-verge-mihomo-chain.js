// Apply this script only to the locally managed Huayun profile.
// Replace the URL and list the exact names of Huayun nodes from that profile.
const PANEL_PROVIDER_URL = 'https://YOUR-PANEL/sub/personal/YOUR-TOKEN/mihomo-provider'
const AIRPORT_NODES = ['花云节点名称 1', '花云节点名称 2']
const LANDING_NODES = {
  'AI-美国': '美国链式落地',
  'X-新加坡': '新加坡链式落地',
  'Maya-菲律宾': '菲律宾链式落地',
  'NodeSeek-德国': '德国链式落地',
}

function main(config) {
  const providerName = 'vps-panel-landing'
  const groupNames = ['机场入口', ...Object.keys(LANDING_NODES)]
  const nodes = Array.isArray(config.proxies) ? config.proxies : []
  const seen = new Set()
  if (!/^https?:\/\//.test(PANEL_PROVIDER_URL) || PANEL_PROVIDER_URL.includes('YOUR-')) throw new Error('请填写 VPS Panel Provider 链接')
  if (Object.values(LANDING_NODES).some(name => !name) || new Set(Object.values(LANDING_NODES)).size !== 4) throw new Error('请填写四个不同的落地节点名称')
  if (!AIRPORT_NODES.length) throw new Error('请填写至少一个花云节点名称')
  for (const name of AIRPORT_NODES) {
    if (seen.has(name)) throw new Error('花云入口名称重复: ' + name)
    seen.add(name)
    const matches = nodes.filter(node => node.name === name)
    if (matches.length !== 1 || matches[0]['dialer-proxy'] || groupNames.includes(name) || Object.values(LANDING_NODES).includes(name)) {
      throw new Error('花云入口必须是本配置中唯一且未配置前置代理的机场节点: ' + name)
    }
  }
  const existingProviders = config['proxy-providers'] || {}
  config['proxy-providers'] = {
    ...existingProviders,
    [providerName]: { type: 'http', url: PANEL_PROVIDER_URL, interval: 3600, proxy: 'DIRECT' },
  }
  const escapeRegex = value => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const regionalGroups = Object.entries(LANDING_NODES).map(([name, node]) => ({
    name, type: 'select', use: [providerName], filter: '^' + escapeRegex(node) + '$', 'empty-fallback': 'REJECT',
  }))
  const originalGroups = Array.isArray(config['proxy-groups']) ? config['proxy-groups'] : []
  config['proxy-groups'] = [
    { name: '机场入口', type: 'select', proxies: [...AIRPORT_NODES] },
    ...regionalGroups,
    ...originalGroups.filter(group => !groupNames.includes(group.name)),
  ]
  const rules = [
    'DOMAIN-SUFFIX,openai.com,AI-美国',
    'DOMAIN-SUFFIX,chatgpt.com,AI-美国',
    'DOMAIN-SUFFIX,x.com,X-新加坡',
    'DOMAIN-SUFFIX,twitter.com,X-新加坡',
    'DOMAIN-SUFFIX,maya.ph,Maya-菲律宾',
    'DOMAIN-SUFFIX,nodeseek.com,NodeSeek-德国',
    'GEOIP,CN,DIRECT',
  ]
  config.rules = [...rules, ...(Array.isArray(config.rules) ? config.rules : []).filter(rule => !rules.includes(rule))]
  return config
}
