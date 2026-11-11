# Routing INI format

VPS Panel accepts a bounded subset of the external configuration syntax used by
[ACL4SSR](https://github.com/ACL4SSR/ACL4SSR/tree/master/Clash/config). This is
not a general MiSub or subconverter parser. Files are UTF-8, at most 10 KiB,
and contain one `[custom]` section; blank lines and lines beginning with `#`
or `;` are ignored. Unknown directives are errors.

``custom_proxy_group=NAME`select`[]MEMBER...`` declares a select group. Members
are named groups or `DIRECT` and `REJECT`. Other members are RE2 regular
expressions matched against eligible node display names in the subscription;
matching is case-sensitive unless the expression uses `(?i)`. The final member
`.*` includes all eligible nodes. Explicit references must precede regular
expressions, and `.*` must be last. There may be at most 16 expressions per
group, each at most 256 bytes. Unsupported backtracking constructs are errors.
Use regex escapes for intentional leading or trailing whitespace; whitespace at
the end of a group line is rejected instead of silently changing the expression.

For example:

```ini
[custom]
custom_proxy_group=日本`select`[]DIRECT`(Japan|JP|Tokyo)
custom_proxy_group=代理`select`[]日本`[]DIRECT
ruleset=日本,[]DOMAIN-SUFFIX,example.com
ruleset=DIRECT,[]GEOIP,CN
ruleset=代理,[]FINAL
```

At render time, each group contains explicit references first, then matching
nodes in subscription order, manual bindings, and finally all nodes if enabled.
Duplicate names are removed. Only currently usable nodes in the relevant
subscription are considered; no stored memberships are rewritten. A group
with no resulting members keeps the existing empty-group error behavior.
The Panel-specific `!!VPS_MANUAL` member represents a group whose nodes come
only from subscription bindings. It cannot be combined with other members and
does not add a fallback; an unbound group still fails rendering.
Select an owned personal subscription or an accessible shared subscriber during
preview to run both production renderers and report patterns with no matches.
Shared previews use saved client state without reconciliation, so later
reconciliation can change the outcome. Without a subscription context, preview
cannot determine which nodes will match.

`ruleset=POLICY,[]GEOIP,CN` creates an inline rule; `[]FINAL` creates `MATCH`.
Inline `no-resolve` is supported. Rules are evaluated in declaration order.
The inline subset supports DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, IP-CIDR,
IP-CIDR6, GEOIP, and RULE-SET. The `routing_rule` extension additionally
supports DOMAIN-WILDCARD, DOMAIN-REGEX, GEOSITE, PROCESS-NAME, PROCESS-PATH,
NETWORK, DST-PORT, and SRC-PORT. Other rule types are rejected; existing
presets using them remain usable but cannot be exported as INI. Mihomo-only
rules generate an explicit Shadowrocket compatibility warning.

The Panel-specific `rule_provider=` directive contains one JSON object with
the existing `name`, `url`, `type`, `behavior`, `format`, and `interval`
fields. `ruleset=POLICY,[]RULE-SET,NAME` refers to it. The `routing_rule=`
directive contains a JSON string representing an existing rule; it preserves
rules whose existing representation cannot be expressed by the shorthand.
Providers must use HTTP, `classical`, `domain`, or `ipcidr` behavior, and
`yaml`, `text`, or `mrs` format; `mrs` supports only `domain` and `ipcidr`,
as documented by [Mihomo](https://github.com/MetaCubeX/Meta-Docs/blob/main/docs/config/rule-providers/index.en.md).
These extensions allow deterministic export without guessing provider formats.
The Panel does not fetch remote URLs during parsing or validation. A bare URL
is rejected because its actual ruleset format is not reliably identifiable
from the URL or filename. INI import and export reject provider URLs containing
userinfo, query parameters, or fragments because they may carry credentials.

For a known classical text ruleset, declare its metadata explicitly:

```ini
[custom]
custom_proxy_group=代理`select`[]DIRECT
rule_provider={"name":"example","url":"https://example.com/rules.list","type":"http","behavior":"classical","format":"text","interval":86400}
ruleset=代理,[]RULE-SET,example
ruleset=代理,[]FINAL
```

Group identity is preserved by name when updating an existing preset. Removing
or renaming a group that still has node bindings is rejected; preview identifies
the affected groups. Updates require the current content revision. Saving the
INI updates the existing routing preset, which remains the source used by both
subscription generators and the visual editor.
