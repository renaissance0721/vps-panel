#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPOSITORY="renaissance0721/vps-panel"
readonly VERSION="__PANEL_VERSION__"
readonly AGENT_DIR="/opt/vps-panel/agent"
readonly BINARY_PATH="${AGENT_DIR}/vps-panel-agent"
readonly CONFIG_FILE="/etc/vps-panel-agent/config.json"
readonly SERVICE_FILE="/etc/systemd/system/vps-panel-agent.service"
readonly SERVICE_NAME="vps-panel-agent.service"

architecture=""
temporary_dir=""
had_binary=false
had_unit=false
changed=false

log() {
  printf '[vps-panel-agent] %s\n' "$*"
}

fail() {
  printf '[vps-panel-agent] Error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$temporary_dir" && -d "$temporary_dir" ]]; then
    rm -rf -- "$temporary_dir"
  fi
}

rollback() {
  [[ "$changed" == true ]] || return 0
  log "Upgrade failed; restoring the previous Agent..."
  if [[ "$had_binary" == true ]]; then
    install -m 0755 "${temporary_dir}/previous-agent" "$BINARY_PATH"
  else
    rm -f -- "$BINARY_PATH"
  fi
  if [[ "$had_unit" == true ]]; then
    install -m 0644 "${temporary_dir}/previous-unit" "$SERVICE_FILE"
  else
    rm -f -- "$SERVICE_FILE"
  fi
  systemctl daemon-reload || true
  systemctl restart "$SERVICE_NAME" || true
}

on_error() {
  rollback
}

[[ ${EUID} -eq 0 ]] || fail "run this upgrader as root"
[[ -s "$CONFIG_FILE" ]] || fail "existing Agent config not found: ${CONFIG_FILE}"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Panel is not running a formal release version"
[[ -d /run/systemd/system ]] || fail "systemd is not running on this VPS"

for command_name in curl uname systemctl install mktemp chmod sha256sum awk; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done

case "$(uname -m)" in
  x86_64 | amd64) architecture="amd64" ;;
  aarch64 | arm64) architecture="arm64" ;;
  *) fail "unsupported architecture: $(uname -m); only amd64 and arm64 are supported" ;;
esac

temporary_dir="$(mktemp -d)"
trap cleanup EXIT
trap on_error ERR

asset_name="vps-panel-agent-linux-${architecture}"
download_base="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
download_path="${temporary_dir}/${asset_name}"
checksums_path="${temporary_dir}/SHA256SUMS"
unit_path="${temporary_dir}/vps-panel-agent.service"

log "Downloading Agent ${VERSION} for linux-${architecture}..."
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 "${download_base}/${asset_name}" -o "$download_path"
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 "${download_base}/SHA256SUMS" -o "$checksums_path"

expected_checksum="$(awk -v asset="$asset_name" '$2 == asset || $2 == "*" asset { print $1; exit }' "$checksums_path")"
[[ "$expected_checksum" =~ ^[0-9a-fA-F]{64}$ ]] || fail "Agent checksum is missing from SHA256SUMS"
actual_checksum="$(sha256sum "$download_path" | awk '{print $1}')"
[[ "${actual_checksum,,}" == "${expected_checksum,,}" ]] || fail "Agent SHA256 checksum does not match"
chmod 0755 "$download_path"
[[ "$("$download_path" version)" == "vps-panel-agent ${VERSION}" ]] || fail "downloaded Agent version does not match ${VERSION}"

cat >"$unit_path" <<EOF
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
ReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system

[Install]
WantedBy=multi-user.target
EOF

install -d -m 0755 "$AGENT_DIR"
if [[ -f "$BINARY_PATH" ]]; then
  install -m 0755 "$BINARY_PATH" "${temporary_dir}/previous-agent"
  had_binary=true
fi
if [[ -f "$SERVICE_FILE" ]]; then
  install -m 0644 "$SERVICE_FILE" "${temporary_dir}/previous-unit"
  had_unit=true
fi

install -m 0755 "$download_path" "${AGENT_DIR}/.vps-panel-agent-upgrade"
mv -f -- "${AGENT_DIR}/.vps-panel-agent-upgrade" "$BINARY_PATH"
install -m 0644 "$unit_path" "$SERVICE_FILE"
changed=true

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
systemctl is-active --quiet "$SERVICE_NAME"
changed=false

log "Agent upgraded to ${VERSION}; existing registration and config were preserved."
