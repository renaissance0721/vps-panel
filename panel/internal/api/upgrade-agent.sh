#!/bin/sh

set -eu

REPOSITORY="renaissance0721/vps-panel"
VERSION="__PANEL_VERSION__"
AGENT_DIR="/opt/vps-panel/agent"
BINARY_PATH="${AGENT_DIR}/vps-panel-agent"
CONFIG_FILE="/etc/vps-panel-agent/config.json"
SERVICE_NAME="vps-panel-agent"

architecture=""
temporary_dir=""
init_system=""
service_file=""
had_binary=false
had_service=false
changed=false

log() {
  printf '[vps-panel-agent] %s\n' "$*"
}

fail() {
  printf '[vps-panel-agent] Error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [ -n "$temporary_dir" ] && [ -d "$temporary_dir" ]; then
    rm -rf "$temporary_dir"
  fi
}

prepare_alpine_dependencies() {
  [ -f /etc/alpine-release ] || return 0
  command -v apk >/dev/null 2>&1 || fail "required command not found: apk"
  if ! command -v curl >/dev/null 2>&1 || [ ! -s /etc/ssl/certs/ca-certificates.crt ]; then
    log "Installing Alpine download dependencies..."
    apk add --no-cache curl ca-certificates
  fi
  if ! command -v install >/dev/null 2>&1; then
    log "Installing Alpine file utility dependency..."
    apk add --no-cache coreutils
  fi
}

detect_init_system() {
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    init_system="systemd"
    service_file="/etc/systemd/system/${SERVICE_NAME}.service"
    return
  fi
  if command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1 && \
    { [ -d /run/openrc ] || [ -x /sbin/openrc-run ]; }; then
    init_system="openrc"
    service_file="/etc/init.d/${SERVICE_NAME}"
    command -v supervise-daemon >/dev/null 2>&1 || fail "required OpenRC supervisor not found: supervise-daemon"
    return
  fi
  fail "unsupported init system: systemd or OpenRC is required"
}

write_systemd_service() {
  service_path=$1
  cat >"$service_path" <<EOF
[Unit]
Description=VPS Panel Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BINARY_PATH}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /opt/vps-panel/realm /etc/vps-panel/realm /etc/systemd/system

[Install]
WantedBy=multi-user.target
EOF
}

write_openrc_service() {
  service_path=$1
  cat >"$service_path" <<EOF
#!/sbin/openrc-run

name="VPS Panel Agent"
description="VPS Panel Agent"
supervisor="supervise-daemon"
command="${BINARY_PATH}"
respawn_delay=3
respawn_max=0
retry="TERM/10/KILL/5"
umask=0077

depend() {
  need net
}
EOF
}

restart_service() {
  if [ "$init_system" = "systemd" ]; then
    systemctl daemon-reload
    systemctl enable "${SERVICE_NAME}.service"
    systemctl restart "${SERVICE_NAME}.service"
    systemctl is-active --quiet "${SERVICE_NAME}.service"
    return
  fi
  rc-update add "$SERVICE_NAME" default
  if rc-service "$SERVICE_NAME" status >/dev/null 2>&1; then
    rc-service "$SERVICE_NAME" restart
  else
    rc-service "$SERVICE_NAME" start
  fi
  rc-service "$SERVICE_NAME" status >/dev/null 2>&1
}

rollback() {
  [ "$changed" = true ] || return 0
  changed=false
  log "Upgrade failed; restoring the previous Agent..."
  if [ "$had_binary" = true ]; then
    install -m 0755 "${temporary_dir}/previous-agent" "$BINARY_PATH" || true
  else
    rm -f "$BINARY_PATH"
  fi
  if [ "$had_service" = true ]; then
    if [ "$init_system" = "systemd" ]; then
      install -m 0644 "${temporary_dir}/previous-service" "$service_file" || true
    else
      install -m 0755 "${temporary_dir}/previous-service" "$service_file" || true
    fi
  else
    rm -f "$service_file"
  fi
  restart_service || true
}

on_exit() {
  status=$?
  if [ "$status" -ne 0 ]; then
    rollback
  fi
  cleanup
  trap - 0
  exit "$status"
}

[ "$(id -u)" -eq 0 ] || fail "run this upgrader as root"
[ -s "$CONFIG_FILE" ] || fail "existing Agent config not found: ${CONFIG_FILE}"
printf '%s\n' "$VERSION" | awk '$0 ~ /^v[0-9]+\.[0-9]+\.[0-9]+$/ { valid=1 } END { exit !valid }' || \
  fail "Panel is not running a formal release version"

prepare_alpine_dependencies
for command_name in curl uname install mktemp chmod sha256sum awk tr id; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done
detect_init_system

case "$(uname -m)" in
  x86_64 | amd64) architecture="amd64" ;;
  aarch64 | arm64) architecture="arm64" ;;
  *) fail "unsupported architecture: $(uname -m); only amd64 and arm64 are supported" ;;
esac

temporary_dir=$(mktemp -d)
trap on_exit 0
trap 'exit 1' HUP INT TERM

asset_name="vps-panel-agent-linux-${architecture}"
download_base="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
download_path="${temporary_dir}/${asset_name}"
checksums_path="${temporary_dir}/SHA256SUMS"
service_path="${temporary_dir}/${SERVICE_NAME}"

log "Downloading Agent ${VERSION} for linux-${architecture}..."
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 "${download_base}/${asset_name}" -o "$download_path"
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 "${download_base}/SHA256SUMS" -o "$checksums_path"

expected_checksum=$(awk -v asset="$asset_name" '$2 == asset || $2 == "*" asset { print $1; exit }' "$checksums_path")
case "$expected_checksum" in
  *[!0-9a-fA-F]* | '') fail "Agent checksum is missing from SHA256SUMS" ;;
esac
[ "${#expected_checksum}" -eq 64 ] || fail "Agent checksum is missing from SHA256SUMS"
actual_checksum=$(sha256sum "$download_path" | awk '{print $1}')
[ "$(printf '%s' "$actual_checksum" | tr 'A-F' 'a-f')" = "$(printf '%s' "$expected_checksum" | tr 'A-F' 'a-f')" ] || \
  fail "Agent SHA256 checksum does not match"
chmod 0755 "$download_path"
[ "$("$download_path" version)" = "vps-panel-agent ${VERSION}" ] || fail "downloaded Agent version does not match ${VERSION}"

if [ "$init_system" = "systemd" ]; then
  write_systemd_service "$service_path"
else
  write_openrc_service "$service_path"
fi

install -d -m 0755 "$AGENT_DIR"
if [ -f "$BINARY_PATH" ]; then
  install -m 0755 "$BINARY_PATH" "${temporary_dir}/previous-agent"
  had_binary=true
fi
if [ -f "$service_file" ]; then
  if [ "$init_system" = "systemd" ]; then
    install -m 0644 "$service_file" "${temporary_dir}/previous-service"
  else
    install -m 0755 "$service_file" "${temporary_dir}/previous-service"
  fi
  had_service=true
fi

changed=true
install -m 0755 "$download_path" "${AGENT_DIR}/.vps-panel-agent-upgrade"
mv -f "${AGENT_DIR}/.vps-panel-agent-upgrade" "$BINARY_PATH"
if [ "$init_system" = "systemd" ]; then
  install -m 0644 "$service_path" "$service_file"
else
  install -m 0755 "$service_path" "$service_file"
fi
restart_service
changed=false

log "Agent upgraded to ${VERSION}; existing registration and config were preserved."
