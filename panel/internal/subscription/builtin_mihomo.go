package subscription

// builtinMihomoTemplate is VPS Panel Default Mihomo Template v1.
const builtinMihomoTemplate = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: true
unified-delay: true
tcp-concurrent: true

profile:
  store-selected: true
  store-fake-ip: true

sniffer:
  enable: true
  sniff:
    HTTP:
      ports:
        - 80
        - 8080-8880
      override-destination: true
    TLS:
      ports:
        - 443
        - 8443
    QUIC:
      ports:
        - 443
        - 8443

dns:
  enable: true
  ipv6: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - "*.local"
    - "geosite:cn"
    - "geosite:private"
  default-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  nameserver:
    - https://1.1.1.1/dns-query
    - https://8.8.8.8/dns-query
  proxy-server-nameserver:
    - https://223.5.5.5/dns-query

proxies: []

proxy-groups:
  - name: 🚀 默认代理
    type: select
    proxies:
      - "{{all}}"
      - DIRECT
  - name: 🤖 AI
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: ▶️ YouTube
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🎬 Netflix
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: ✈️ Telegram
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🎵 TikTok
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🍎 Apple
    type: select
    proxies:
      - DIRECT
      - 🚀 默认代理
      - "{{all}}"
  - name: Ⓜ️ Microsoft
    type: select
    proxies:
      - DIRECT
      - 🚀 默认代理
      - "{{all}}"

rule-providers:
  OpenAI:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/OpenAI/OpenAI.yaml
  Claude:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Claude/Claude.yaml
  Gemini:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Gemini/Gemini.yaml
  YouTube:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/YouTube/YouTube.yaml
  Netflix:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Netflix/Netflix_Classical.yaml
  Telegram:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Telegram/Telegram.yaml
  TikTok:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/TikTok/TikTok.yaml
  Apple:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Apple/Apple_Classical.yaml
  Copilot:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Copilot/Copilot.yaml
  Microsoft:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Microsoft/Microsoft.yaml

rules:
  - "RULE-SET,OpenAI,🤖 AI"
  - "RULE-SET,Claude,🤖 AI"
  - "RULE-SET,Gemini,🤖 AI"
  - "RULE-SET,YouTube,▶️ YouTube"
  - "RULE-SET,Netflix,🎬 Netflix"
  - "RULE-SET,Telegram,✈️ Telegram"
  - "RULE-SET,TikTok,🎵 TikTok"
  - "RULE-SET,Apple,🍎 Apple"
  - "RULE-SET,Copilot,Ⓜ️ Microsoft"
  - "RULE-SET,Microsoft,Ⓜ️ Microsoft"
  - "GEOIP,CN,DIRECT,no-resolve"
  - "MATCH,🚀 默认代理"
`
