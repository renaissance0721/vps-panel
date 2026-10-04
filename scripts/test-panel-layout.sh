#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
sandbox="$(mktemp -d -t panel-layout-test.XXXXXXXX)"
case "$sandbox" in
  /tmp/panel-layout-test.*) ;;
  *) printf 'unsafe test directory: %s\n' "$sandbox" >&2; exit 1 ;;
esac
trap 'rm -rf -- "$sandbox"' EXIT

mock_bin="${sandbox}/bin"
mkdir -p "$mock_bin"
export PATH="${mock_bin}:${PATH}"

cat >"${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
output=""
arguments="$*"
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "-o" ]]; then output="$2"; shift 2; else shift; fi
done
if [[ -n "$output" ]]; then
  case "$output" in
    *.tar.gz) cp "$PANEL_TEST_ARCHIVE" "$output" ;;
    */vp) cp "$PANEL_TEST_VP" "$output" ;;
    *) cp "$PANEL_TEST_INSTALLER" "$output" ;;
  esac
else
  [[ "${PANEL_TEST_HEALTH_FAIL:-0}" != 1 ]] || exit 22
  if [[ "$arguments" == *"https://"* ]]; then
    [[ "${PANEL_TEST_HTTPS_FAIL:-0}" != 1 ]] || exit 22
  fi
  printf '{"status":"ok","database":"ok"}\n'
fi
EOF
cat >"${mock_bin}/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$PANEL_TEST_SYSTEMCTL_LOG"
if [[ "$*" == "enable --now vps-panel.service" && "${PANEL_TEST_MUTATE_DB_ON_START:-0}" == 1 && ! -e "${PANEL_TEST_ROOT}/new-db-written" ]]; then
  printf 'new-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
  touch "${PANEL_TEST_ROOT}/new-db-written"
fi
[[ "${1:-}" != "is-active" ]]
EOF
cat >"${mock_bin}/install" <<'EOF'
#!/usr/bin/env bash
args=()
directory=0
mode=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -d) directory=1; shift ;;
    -m) mode="$2"; shift 2 ;;
    -o | -g) shift 2 ;;
    *) args+=("$1"); shift ;;
  esac
done
if [[ "$directory" -eq 1 ]]; then
  mkdir -p -- "${args[@]}"
else
  cp -- "${args[0]}" "${args[1]}"
  if [[ "$mode" == 0755 ]]; then chmod +x -- "${args[1]}"; fi
fi
EOF
cat >"${mock_bin}/id" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-u" && "${2:-}" == "vps-panel" ]]; then
  printf '1000\n'
  exit 0
fi
exec /usr/bin/id "$@"
EOF
for command_name in getent groupadd useradd chown journalctl sleep; do
  printf '#!/usr/bin/env bash\nexit 0\n' >"${mock_bin}/${command_name}"
done
cat >"${mock_bin}/caddy" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$PANEL_TEST_CADDY_LOG"
exit 0
EOF
chmod +x "${mock_bin}"/*

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_exists() { [[ -e "$1" ]] || fail "missing $1"; }
assert_absent() { [[ ! -e "$1" ]] || fail "unexpected $1"; }
assert_content() { [[ "$(cat "$1")" == "$2" ]] || fail "unexpected contents in $1"; }
assert_binary_version() { grep -Fqx "# $2" "$1" || fail "unexpected Panel binary in $1"; }
assert_config() { grep -Fqx "$2=$3" "$1" || fail "$2 was not $3 in $1"; }
assert_log_absent() { ! grep -Fq "$2" "$1" || fail "unexpected '$2' in $1"; }

transform_script() {
  local source="$1" destination="$2"
  sed \
    -e "s@/opt/vps-panel@${PANEL_TEST_ROOT}/opt/vps-panel@g" \
    -e "s@/var/lib/vps-panel@${PANEL_TEST_ROOT}/var/lib/vps-panel@g" \
    -e "s@/etc/vps-panel@${PANEL_TEST_ROOT}/etc/vps-panel@g" \
    -e "s@/etc/systemd/system@${PANEL_TEST_ROOT}/etc/systemd/system@g" \
    -e "s@/etc/caddy@${PANEL_TEST_ROOT}/etc/caddy@g" \
    -e "s@/usr/local/bin@${PANEL_TEST_ROOT}/usr/local/bin@g" \
    -e "s@/run/systemd/system@${PANEL_TEST_ROOT}/run/systemd/system@g" \
    -e 's@^if \[\[ ${EUID} -ne 0 \]\]; then$@if false; then@' \
    -e 's@\[\[ ${EUID} -eq 0 \]\] || fail "run vp as root"@true@' \
    "$source" >"$destination"
}

make_archive() {
  local version="$1"
  local payload="${sandbox}/payload"
  mkdir -p "${payload}/web"
  printf '#!/bin/sh\n# %s\nif [ "${1:-}" = database-backup-for-upgrade ]; then cp "${PANEL_DATA_DIR}/panel.db" "$2"; fi\nexit 0\n' "$version" >"${payload}/vps-panel"
  printf '%s\n' "$version" >"${payload}/web/index.html"
  printf '%s\n' "$version" >"${payload}/web/${version}.js"
  tar -C "$payload" -czf "$PANEL_TEST_ARCHIVE" vps-panel web
  rm -f -- "${payload}/web/"*.js
}

case_number=0
new_fixture() {
  case_number=$((case_number + 1))
  export PANEL_TEST_ROOT="${sandbox}/case-${case_number}"
  export PANEL_TEST_INSTALLER="${sandbox}/installer-${case_number}.sh"
  export PANEL_TEST_VP="${sandbox}/vp-${case_number}.sh"
  export PANEL_TEST_ARCHIVE="${sandbox}/archive-${case_number}.tar.gz"
  export PANEL_TEST_SYSTEMCTL_LOG="${sandbox}/systemctl-${case_number}.log"
  export PANEL_TEST_CADDY_LOG="${sandbox}/caddy-${case_number}.log"
  export PANEL_TEST_HEALTH_FAIL=0
  export PANEL_TEST_HTTPS_FAIL=0
  export PANEL_TEST_MUTATE_DB_ON_START=0
  mkdir -p "${PANEL_TEST_ROOT}/run/systemd/system" "${PANEL_TEST_ROOT}/etc/systemd/system"
  : >"$PANEL_TEST_SYSTEMCTL_LOG"
  : >"$PANEL_TEST_CADDY_LOG"
  transform_script "${repo_root}/scripts/install-panel.sh" "$PANEL_TEST_INSTALLER"
  transform_script "${repo_root}/scripts/vp" "$PANEL_TEST_VP"
  make_archive version-one
}

agent_paths=(
  opt/vps-panel/agent/identity
  opt/vps-panel/xray/binary
  opt/vps-panel/realm/binary
  opt/vps-panel/acme/script
  var/lib/vps-panel/acme/account
  etc/vps-panel/xray/config.json
  etc/vps-panel/realm/config.toml
  etc/vps-panel-agent/config.json
)
create_agent_files() {
  local relative=""
  for relative in "${agent_paths[@]}"; do
    mkdir -p "$(dirname "${PANEL_TEST_ROOT}/${relative}")"
    printf 'agent-owned\n' >"${PANEL_TEST_ROOT}/${relative}"
  done
}
assert_agent_files() {
  local relative=""
  for relative in "${agent_paths[@]}"; do
    assert_content "${PANEL_TEST_ROOT}/${relative}" agent-owned
  done
}

run_install() { bash "$PANEL_TEST_INSTALLER" --domain "${1:-:80}" >/dev/null; }
run_installer() { bash "$PANEL_TEST_INSTALLER" "$@" >/dev/null; }

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_config "$config" PANEL_DOMAIN panel.example.com
assert_config "$config" PANEL_PROXY_MODE caddy
assert_config "$config" PANEL_LISTEN_ADDR 127.0.0.1:8080
assert_exists "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
grep -Fq 'reverse_proxy 127.0.0.1:8080' "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy" || \
  fail 'Caddy upstream was not the Panel listen endpoint'
printf 'ok: clean Caddy install uses the private listen default\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain www.yurucamp.vip --proxy-mode external
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_config "$config" PANEL_DOMAIN www.yurucamp.vip
assert_config "$config" PANEL_PROXY_MODE external
assert_config "$config" PANEL_LISTEN_ADDR 0.0.0.0:8080
assert_absent "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'external install invoked Caddy'
printf 'ok: clean external install leaves Caddy untouched\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain www.yurucamp.vip --proxy-mode external --listen-addr 0.0.0.0:8080
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" update >/dev/null
export PANEL_TEST_MUTATE_DB_ON_START=0
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-two
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" new-db
assert_config "$config" PANEL_DOMAIN www.yurucamp.vip
assert_config "$config" PANEL_PROXY_MODE external
assert_config "$config" PANEL_LISTEN_ADDR 0.0.0.0:8080
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'external update invoked Caddy'
printf 'ok: external update preserves domain, listen address, and proxy mode\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" update >/dev/null
export PANEL_TEST_MUTATE_DB_ON_START=0
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-two
assert_config "$config" PANEL_PROXY_MODE caddy
assert_config "$config" PANEL_LISTEN_ADDR 127.0.0.1:8080
assert_exists "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
grep -Fq 'reload caddy.service' "$PANEL_TEST_SYSTEMCTL_LOG" || fail 'Caddy update did not reload Caddy'
grep -Fq 'validate --config' "$PANEL_TEST_CADDY_LOG" || fail 'Caddy update was not validated'
printf 'ok: Caddy update retains managed reverse proxy behavior\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain old.example.com --proxy-mode external
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" domain new.example.com >/dev/null
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_config "$config" PANEL_DOMAIN new.example.com
assert_config "$config" PANEL_PROXY_MODE external
assert_config "$config" PANEL_LISTEN_ADDR 0.0.0.0:8080
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'external domain change invoked Caddy'
: >"$PANEL_TEST_SYSTEMCTL_LOG"
bash "$PANEL_TEST_VP" restart >/dev/null
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'external restart invoked Caddy'
printf 'ok: external domain change preserves proxy mode\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
: >"$PANEL_TEST_SYSTEMCTL_LOG"
bash "$PANEL_TEST_VP" proxy external 0.0.0.0:8080 >/dev/null
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_config "$config" PANEL_PROXY_MODE external
assert_config "$config" PANEL_LISTEN_ADDR 0.0.0.0:8080
assert_absent "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" 'other.example.com { respond OK }'
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" 'disable --now caddy.service'
printf 'ok: Caddy to external removes only Panel Caddy configuration\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode external
bash "$PANEL_TEST_VP" proxy caddy 127.0.0.1:8080 >/dev/null
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
assert_config "$config" PANEL_PROXY_MODE caddy
assert_exists "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
grep -Fqx "import ${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy" \
  "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" || fail 'external to Caddy did not add the managed import'
printf 'ok: external to Caddy creates the managed reverse proxy\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode external
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
printf 'uninstall\nn\n' | bash "$PANEL_TEST_VP" uninstall >/dev/null
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'external uninstall invoked Caddy'
printf 'ok: external uninstall leaves Caddy untouched\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
printf 'uninstall\nn\n' | bash "$PANEL_TEST_VP" uninstall >/dev/null
assert_absent "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" 'other.example.com { respond OK }'
printf 'ok: Caddy uninstall removes only the Panel snippet and import\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain www.yurucamp.vip --proxy-mode external --listen-addr 0.0.0.0:8080
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
cp "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" "${sandbox}/external-environment.before"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
export PANEL_TEST_HEALTH_FAIL=1
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
if bash "$PANEL_TEST_VP" update >/dev/null; then fail 'failed external update unexpectedly succeeded'; fi
export PANEL_TEST_HEALTH_FAIL=0
export PANEL_TEST_MUTATE_DB_ON_START=0
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-one
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" old-db
cmp -s "${sandbox}/external-environment.before" "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" || \
  fail 'failed external update did not restore the environment'
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'failed external update invoked Caddy'
printf 'ok: failed external update restores Panel and never touches Caddy\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
printf 'old-managed-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
cp "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" "${sandbox}/caddyfile.before"
cp "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" "${sandbox}/caddy-environment.before"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
export PANEL_TEST_HTTPS_FAIL=1
if bash "$PANEL_TEST_VP" update >/dev/null; then fail 'failed Caddy update unexpectedly succeeded'; fi
export PANEL_TEST_HTTPS_FAIL=0
export PANEL_TEST_MUTATE_DB_ON_START=0
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-one
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" old-db
assert_content "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy" old-managed-caddy
cmp -s "${sandbox}/caddyfile.before" "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" || \
  fail 'failed Caddy update did not restore Caddyfile'
cmp -s "${sandbox}/caddy-environment.before" "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" || \
  fail 'failed Caddy update did not restore the environment'
printf 'ok: failed Caddy update restores Panel, database, environment, and Caddy\n'

new_fixture
run_installer --domain :80 --proxy-mode external
sed -i '/^PANEL_PROXY_MODE=/d' "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
make_archive version-two
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" update >/dev/null
assert_config "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" PANEL_PROXY_MODE external
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'legacy :80 update invoked Caddy'
printf 'ok: legacy :80 install is inferred as external\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain panel.example.com --proxy-mode caddy
sed -i '/^PANEL_PROXY_MODE=/d' "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
make_archive version-two
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" update >/dev/null
assert_config "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" PANEL_PROXY_MODE caddy
grep -Fq 'validate --config' "$PANEL_TEST_CADDY_LOG" || fail 'legacy managed Caddy was not inferred'
printf 'ok: legacy Panel-owned Caddy install is inferred as Caddy\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'user-owned-caddy\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_installer --domain legacy.example.com --proxy-mode external
sed -i '/^PANEL_PROXY_MODE=/d' "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
make_archive version-two
: >"$PANEL_TEST_SYSTEMCTL_LOG"
: >"$PANEL_TEST_CADDY_LOG"
bash "$PANEL_TEST_VP" update >/dev/null
assert_config "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" PANEL_PROXY_MODE external
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" user-owned-caddy
assert_log_absent "$PANEL_TEST_SYSTEMCTL_LOG" caddy.service
[[ ! -s "$PANEL_TEST_CADDY_LOG" ]] || fail 'legacy external update invoked Caddy'
printf 'ok: legacy real domain without Panel Caddy ownership is inferred as external\n'

new_fixture
if run_installer --domain panel.example.com --proxy-mode invalid; then
  fail 'invalid proxy mode unexpectedly succeeded'
fi
if run_installer --domain panel.example.com --proxy-mode external --listen-addr invalid; then
  fail 'invalid listen address unexpectedly succeeded'
fi
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel"
printf 'ok: invalid proxy mode and listen address are rejected\n'

new_fixture
run_install
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-one
assert_exists "${PANEL_TEST_ROOT}/opt/vps-panel/panel/.vps-panel-install"
assert_exists "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel"
assert_exists "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
config="${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment"
grep -Fqx "PANEL_DATA_DIR=${PANEL_TEST_ROOT}/var/lib/vps-panel/panel" "$config" || fail 'Panel data path missing'
grep -Fqx "PANEL_WEB_DIR=${PANEL_TEST_ROOT}/opt/vps-panel/panel/web" "$config" || fail 'Panel web path missing'
service="${PANEL_TEST_ROOT}/etc/systemd/system/vps-panel.service"
for expected in \
  "EnvironmentFile=${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" \
  "WorkingDirectory=${PANEL_TEST_ROOT}/opt/vps-panel/panel" \
  "ExecStart=${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" \
  "ReadWritePaths=${PANEL_TEST_ROOT}/var/lib/vps-panel/panel" \
  'User=vps-panel' 'Group=vps-panel' 'NoNewPrivileges=true' \
  'PrivateTmp=true' 'ProtectHome=true' 'ProtectSystem=full'; do
  grep -Fqx "$expected" "$service" || fail "service misses $expected"
done
printf 'ok: clean install and service paths\n'

new_fixture
create_agent_files
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_install
assert_agent_files
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
bash "$PANEL_TEST_VP" update >/dev/null
export PANEL_TEST_MUTATE_DB_ON_START=0
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-two
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" new-db
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/panel/web/version-one.js"
assert_agent_files
printf 'panel-data\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
printf 'uninstall\nn\n' | bash "$PANEL_TEST_VP" uninstall >/dev/null
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/panel"
assert_absent "${PANEL_TEST_ROOT}/etc/vps-panel/panel"
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" panel-data
assert_agent_files
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" 'other.example.com { respond OK }'
run_install
printf 'uninstall\ny\n' | bash "$PANEL_TEST_VP" uninstall >/dev/null
assert_absent "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel"
assert_agent_files
printf 'ok: Agent coexistence, Panel update, both uninstall choices\n'

new_fixture
run_install
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db"
make_archive version-two
export PANEL_TEST_MUTATE_DB_ON_START=1
export PANEL_TEST_HEALTH_FAIL=1
if run_install; then fail 'unhealthy upgrade unexpectedly succeeded'; fi
export PANEL_TEST_HEALTH_FAIL=0
export PANEL_TEST_MUTATE_DB_ON_START=0
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-one
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db" old-db
failed_databases=("${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/panel.db.failed-upgrade-"*)
[[ ${#failed_databases[@]} -eq 1 && -f "${failed_databases[0]}" ]] || fail 'failed upgrade database was not retained'
assert_content "${failed_databases[0]}" new-db
printf 'ok: failed upgrade restores old binary and SQLite snapshot\n'

new_fixture
create_agent_files
mkdir -p "${PANEL_TEST_ROOT}/opt/vps-panel/web" "${PANEL_TEST_ROOT}/var/lib/vps-panel/restore" \
  "${PANEL_TEST_ROOT}/etc/vps-panel" "${PANEL_TEST_ROOT}/etc/systemd/system"
printf 'old-binary\n' >"${PANEL_TEST_ROOT}/opt/vps-panel/vps-panel"
printf 'old-web\n' >"${PANEL_TEST_ROOT}/opt/vps-panel/web/index.html"
touch "${PANEL_TEST_ROOT}/opt/vps-panel/.vps-panel-install"
for name in panel.db panel.db-wal panel.db-shm; do
  printf 'old-%s\n' "$name" >"${PANEL_TEST_ROOT}/var/lib/vps-panel/${name}"
done
printf 'pending\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/restore/pending.json"
printf 'PANEL_DOMAIN=panel.example.com\n' >"${PANEL_TEST_ROOT}/etc/vps-panel/environment"
printf 'old-unit\n' >"${PANEL_TEST_ROOT}/etc/systemd/system/vps-panel.service"
bash "$PANEL_TEST_INSTALLER" >/dev/null
assert_binary_version "${PANEL_TEST_ROOT}/opt/vps-panel/panel/vps-panel" version-one
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/vps-panel"
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/web"
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/.vps-panel-install"
assert_absent "${PANEL_TEST_ROOT}/etc/vps-panel/environment"
grep -Fqx 'PANEL_DOMAIN=panel.example.com' "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" || \
  fail 'legacy domain was not retained'
assert_config "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" PANEL_PROXY_MODE external
assert_config "${PANEL_TEST_ROOT}/etc/vps-panel/panel/environment" PANEL_LISTEN_ADDR 0.0.0.0:8080
for name in panel.db panel.db-wal panel.db-shm; do
  assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/${name}" "old-${name}"
  assert_absent "${PANEL_TEST_ROOT}/var/lib/vps-panel/${name}"
done
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel/restore/pending.json" pending
assert_agent_files
printf 'ok: mixed legacy Panel and Agent migration\n'

new_fixture
create_agent_files
mkdir -p "${PANEL_TEST_ROOT}/opt/vps-panel/web" "${PANEL_TEST_ROOT}/var/lib/vps-panel" \
  "${PANEL_TEST_ROOT}/etc/vps-panel" "${PANEL_TEST_ROOT}/etc/systemd/system"
printf 'old-binary\n' >"${PANEL_TEST_ROOT}/opt/vps-panel/vps-panel"
printf 'old-web\n' >"${PANEL_TEST_ROOT}/opt/vps-panel/web/index.html"
touch "${PANEL_TEST_ROOT}/opt/vps-panel/.vps-panel-install"
printf 'old-db\n' >"${PANEL_TEST_ROOT}/var/lib/vps-panel/panel.db"
printf 'PANEL_DOMAIN=:80\n' >"${PANEL_TEST_ROOT}/etc/vps-panel/environment"
printf 'old-unit\n' >"${PANEL_TEST_ROOT}/etc/systemd/system/vps-panel.service"
export PANEL_TEST_HEALTH_FAIL=1
if run_install; then fail 'unhealthy install unexpectedly succeeded'; fi
export PANEL_TEST_HEALTH_FAIL=0
assert_content "${PANEL_TEST_ROOT}/opt/vps-panel/vps-panel" old-binary
assert_content "${PANEL_TEST_ROOT}/opt/vps-panel/web/index.html" old-web
assert_exists "${PANEL_TEST_ROOT}/opt/vps-panel/.vps-panel-install"
assert_absent "${PANEL_TEST_ROOT}/opt/vps-panel/panel"
assert_content "${PANEL_TEST_ROOT}/var/lib/vps-panel/panel.db" old-db
assert_content "${PANEL_TEST_ROOT}/etc/vps-panel/environment" 'PANEL_DOMAIN=:80'
assert_content "${PANEL_TEST_ROOT}/etc/systemd/system/vps-panel.service" old-unit
assert_agent_files
printf 'ok: failed migration rolls back only Panel files\n'

new_fixture
create_agent_files
mkdir -p "${PANEL_TEST_ROOT}/opt/vps-panel/panel"
printf 'unrelated\n' >"${PANEL_TEST_ROOT}/opt/vps-panel/panel/other-file"
if run_install; then fail 'unowned Panel directory unexpectedly replaced'; fi
assert_content "${PANEL_TEST_ROOT}/opt/vps-panel/panel/other-file" unrelated
assert_agent_files
printf 'ok: unrelated Panel directory is rejected without touching Agent\n'

new_fixture
mkdir -p "${PANEL_TEST_ROOT}/etc/caddy"
printf 'other.example.com { respond OK }\n' >"${PANEL_TEST_ROOT}/etc/caddy/Caddyfile"
run_install panel.example.com
assert_exists "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
grep -Fqx "import ${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy" "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" || \
  fail 'Caddy import missing'
printf 'uninstall\nn\n' | bash "$PANEL_TEST_VP" uninstall >/dev/null
assert_absent "${PANEL_TEST_ROOT}/etc/caddy/vps-panel.caddy"
assert_content "${PANEL_TEST_ROOT}/etc/caddy/Caddyfile" 'other.example.com { respond OK }'
printf 'ok: Caddy site coexistence\n'
