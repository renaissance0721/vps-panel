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

rules:
  - "GEOSITE,category-ai-!cn,🤖 AI"
  - "GEOSITE,youtube,▶️ YouTube"
  - "GEOSITE,netflix,🎬 Netflix"
  - "GEOSITE,telegram,✈️ Telegram"
  - "GEOSITE,tiktok,🎵 TikTok"
  - "GEOSITE,apple,🍎 Apple"
  - "GEOSITE,microsoft,Ⓜ️ Microsoft"
  - "GEOSITE,private,DIRECT"
  - "GEOSITE,cn,DIRECT"
  - "GEOIP,CN,DIRECT,no-resolve"
  - "MATCH,🚀 默认代理"
`
