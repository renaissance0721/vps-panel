#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPOSITORY_ARCHIVE="https://github.com/renaissance0721/vps-panel/archive/refs/heads/main.tar.gz"
readonly INSTALL_DIR="/opt/vps-panel"
readonly INSTALL_MARKER="${INSTALL_DIR}/.vps-panel-install"

requested_domain=""

log() {
  printf '[vps-panel] %s\n' "$*"
}

fail() {
  printf '[vps-panel] Error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: install-panel.sh [--domain panel.example.com]

Options:
  --domain DOMAIN  Configure Caddy automatic HTTPS for this domain.
  -h, --help       Show this help message.
EOF
}

validate_domain() {
  local value="$1"
  local domain_pattern='^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$'

  if [[ "$value" != ":80" && ! "$value" =~ $domain_pattern ]]; then
    fail "invalid domain '${value}'; use a hostname such as panel.example.com"
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)
      [[ $# -ge 2 ]] || fail "--domain requires a value"
      requested_domain="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

if [[ -n "$requested_domain" ]]; then
  validate_domain "$requested_domain"
fi

if [[ ${EUID} -ne 0 ]]; then
  fail "run this installer as root, for example: curl ... | sudo bash"
fi

for command_name in curl tar docker; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done

docker compose version >/dev/null 2>&1 || fail "Docker Compose plugin is required"
docker info >/dev/null 2>&1 || fail "Docker daemon is not running"

if [[ -d "$INSTALL_DIR" && ! -f "$INSTALL_MARKER" ]]; then
  if [[ -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    fail "${INSTALL_DIR} already exists and was not created by this installer"
  fi
fi

temporary_dir="$(mktemp -d)"
trap 'rm -rf -- "$temporary_dir"' EXIT

archive_path="${temporary_dir}/vps-panel.tar.gz"
source_dir="${temporary_dir}/vps-panel-main"

log "Downloading VPS Panel..."
curl --proto '=https' --tlsv1.2 -fsSL "$REPOSITORY_ARCHIVE" -o "$archive_path"
tar -xzf "$archive_path" -C "$temporary_dir"
[[ -d "$source_dir" ]] || fail "downloaded archive has an unexpected layout"

install -d -m 0755 "$INSTALL_DIR"
cp -a "${source_dir}/." "$INSTALL_DIR/"
touch "$INSTALL_MARKER"

environment_file="${INSTALL_DIR}/deploy/.env"
if [[ -n "$requested_domain" ]]; then
  printf 'PANEL_DOMAIN=%s\n' "$requested_domain" >"$environment_file"
elif [[ ! -f "$environment_file" ]]; then
  printf 'PANEL_DOMAIN=:80\n' >"$environment_file"
fi
chmod 0600 "$environment_file"

cd "${INSTALL_DIR}/deploy"
log "Building and starting containers..."
docker compose up -d --build

log "Waiting for Panel health check..."
for _ in {1..30}; do
  if docker compose exec -T panel /app/vps-panel healthcheck >/dev/null 2>&1; then
    configured_domain="$(sed -n 's/^PANEL_DOMAIN=//p' "$environment_file" | tail -n 1)"
    if [[ "$configured_domain" == ":80" ]]; then
      log "Installation complete. Open http://YOUR_VPS_IP"
    else
      log "Installation complete. Open https://${configured_domain}"
    fi
    exit 0
  fi
  sleep 2
done

docker compose ps >&2
docker compose logs --tail=50 panel caddy >&2
fail "Panel did not become healthy within 60 seconds"
