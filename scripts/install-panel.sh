#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPOSITORY="renaissance0721/vps-panel"
readonly RELEASE_DOWNLOAD_BASE="https://github.com/${REPOSITORY}/releases/latest/download"
readonly REMOTE_MANAGER="https://raw.githubusercontent.com/${REPOSITORY}/main/scripts/vp"
readonly INSTALL_DIR="/opt/vps-panel"
readonly INSTALL_MARKER="${INSTALL_DIR}/.vps-panel-install"
readonly DATA_DIR="/var/lib/vps-panel"
readonly CONFIG_DIR="/etc/vps-panel"
readonly CONFIG_FILE="${CONFIG_DIR}/environment"
readonly SERVICE_FILE="/etc/systemd/system/vps-panel.service"
readonly COMMAND_PATH="/usr/local/bin/vp"
readonly CADDY_FILE="/etc/caddy/Caddyfile"
readonly CADDY_SNIPPET="/etc/caddy/vps-panel.caddy"
readonly CADDY_IMPORT="import /etc/caddy/vps-panel.caddy"

requested_domain=""
temporary_dir=""
staged_dir=""
backup_dir=""
architecture=""
listen_address=""
had_existing_install=0
install_swapped=0
legacy_docker_install=0
legacy_docker_stopped=0
rollback_needed=0
config_existed=0
unit_existed=0
caddy_changed=0
caddy_main_existed=0
caddy_snippet_existed=0
caddy_installed=0

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
  --domain DOMAIN  Configure native Caddy automatic HTTPS for this domain.
  -h, --help       Show this help message.

Without a domain, Panel listens publicly on port 8080 for direct IP access.
EOF
}

validate_domain() {
  local value="$1"
  local domain_pattern='^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$'

  if [[ "$value" != ":80" && ! "$value" =~ $domain_pattern ]]; then
    fail "invalid domain '${value}'; use a hostname such as panel.example.com"
  fi
}

current_domain() {
  local domain=""

  if [[ -f "$CONFIG_FILE" ]]; then
    domain="$(sed -n 's/^PANEL_DOMAIN=//p' "$CONFIG_FILE" | tail -n 1)"
  elif [[ -f "${INSTALL_DIR}/deploy/.env" ]]; then
    domain="$(sed -n 's/^PANEL_DOMAIN=//p' "${INSTALL_DIR}/deploy/.env" | tail -n 1)"
  fi

  printf '%s\n' "$domain"
}

choose_domain() {
  local existing_domain=""
  local entered_domain=""

  existing_domain="$(current_domain)"
  if [[ -z "$requested_domain" && -t 1 && -r /dev/tty ]]; then
    if [[ -n "$existing_domain" ]]; then
      printf '[vps-panel] Panel domain [%s]: ' "$existing_domain" >/dev/tty
    else
      printf '[vps-panel] Panel domain (leave empty for IP:8080): ' >/dev/tty
    fi
    IFS= read -r entered_domain </dev/tty || true
  fi

  if [[ -n "$entered_domain" ]]; then
    requested_domain="$entered_domain"
  elif [[ -z "$requested_domain" && -n "$existing_domain" ]]; then
    requested_domain="$existing_domain"
  elif [[ -z "$requested_domain" ]]; then
    requested_domain=":80"
  fi

  validate_domain "$requested_domain"
  if [[ "$requested_domain" == ":80" ]]; then
    listen_address="0.0.0.0:8080"
  else
    listen_address="127.0.0.1:8080"
  fi
}

detect_architecture() {
  case "$(uname -m)" in
    x86_64 | amd64) architecture="amd64" ;;
    aarch64 | arm64) architecture="arm64" ;;
    *) fail "unsupported architecture: $(uname -m); only amd64 and arm64 are supported" ;;
  esac
}

wait_for_panel() {
  local response=""

  for _ in {1..30}; do
    if response="$(curl -fsS --connect-timeout 2 --max-time 4 \
      http://127.0.0.1:8080/api/health 2>/dev/null)"; then
      if [[ "$response" == *'"status":"ok"'* ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

wait_for_https() {
  local response=""

  for _ in {1..45}; do
    if response="$(curl --proto '=https' --tlsv1.2 -fsS \
      --connect-timeout 2 --max-time 5 \
      "https://${requested_domain}/api/health" 2>/dev/null)"; then
      if [[ "$response" == *'"status":"ok"'* ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

restore_file() {
  local destination="$1"
  local backup="$2"
  local existed="$3"

  if [[ "$existed" -eq 1 ]]; then
    cp -a "$backup" "$destination" || true
  else
    rm -f -- "$destination" || true
  fi
}

restore_caddy() {
  [[ "$caddy_changed" -eq 1 ]] || return 0

  restore_file "$CADDY_FILE" "${temporary_dir}/Caddyfile.backup" "$caddy_main_existed"
  restore_file "$CADDY_SNIPPET" "${temporary_dir}/vps-panel.caddy.backup" "$caddy_snippet_existed"
  if [[ "$caddy_installed" -eq 1 ]]; then
    systemctl disable --now caddy.service >/dev/null 2>&1 || true
  elif command -v caddy >/dev/null 2>&1; then
    caddy validate --config "$CADDY_FILE" >/dev/null 2>&1 || true
    systemctl reload caddy.service >/dev/null 2>&1 || true
  fi
}

rollback_install() {
  log "Restoring the previous installation..."
  if [[ "$had_existing_install" -eq 0 || "$legacy_docker_install" -eq 1 ]]; then
    systemctl disable --now vps-panel.service >/dev/null 2>&1 || true
  else
    systemctl stop vps-panel.service >/dev/null 2>&1 || true
  fi

  if [[ "$install_swapped" -eq 1 ]]; then
    [[ "$INSTALL_DIR" == "/opt/vps-panel" ]] || return 0
    rm -rf -- "$INSTALL_DIR" || true
  fi
  if [[ "$had_existing_install" -eq 1 && -d "$backup_dir" && ! -e "$INSTALL_DIR" ]]; then
    mv "$backup_dir" "$INSTALL_DIR" || true
  fi

  restore_file "$CONFIG_FILE" "${temporary_dir}/environment.backup" "$config_existed"
  restore_file "$SERVICE_FILE" "${temporary_dir}/vps-panel.service.backup" "$unit_existed"
  systemctl daemon-reload >/dev/null 2>&1 || true
  restore_caddy

  if [[ "$legacy_docker_stopped" -eq 1 && -f "${INSTALL_DIR}/deploy/docker-compose.yml" ]]; then
    docker compose \
      --project-directory "${INSTALL_DIR}/deploy" \
      -f "${INSTALL_DIR}/deploy/docker-compose.yml" \
      up -d >/dev/null 2>&1 || true
  elif [[ "$had_existing_install" -eq 1 ]]; then
    systemctl start vps-panel.service >/dev/null 2>&1 || true
  else
    systemctl disable vps-panel.service >/dev/null 2>&1 || true
  fi
}

cleanup() {
  if [[ -n "$temporary_dir" && -d "$temporary_dir" ]]; then
    rm -rf -- "$temporary_dir"
  fi
  if [[ -n "$staged_dir" && -d "$staged_dir" ]]; then
    rm -rf -- "$staged_dir"
  fi
  if [[ "$rollback_needed" -eq 0 && -n "$backup_dir" && -d "$backup_dir" ]]; then
    rm -rf -- "$backup_dir"
  fi
}

on_exit() {
  local exit_code=$?
  trap - EXIT
  if [[ "$exit_code" -ne 0 && "$rollback_needed" -eq 1 ]]; then
    rollback_install
  fi
  cleanup
  exit "$exit_code"
}

ensure_service_user() {
  if ! getent group vps-panel >/dev/null 2>&1; then
    groupadd --system vps-panel
  fi
  if ! id -u vps-panel >/dev/null 2>&1; then
    useradd --system \
      --gid vps-panel \
      --home-dir "$DATA_DIR" \
      --no-create-home \
      --shell /usr/sbin/nologin \
      vps-panel
  fi
  install -d -o vps-panel -g vps-panel -m 0750 "$DATA_DIR"
}

migrate_legacy_docker_data() {
  local volume_path=""

  [[ -f "${INSTALL_DIR}/deploy/docker-compose.yml" ]] || return 0
  legacy_docker_install=1
  command -v docker >/dev/null 2>&1 || fail "the legacy Docker installation was found, but Docker is unavailable for migration"
  docker compose version >/dev/null 2>&1 || fail "the legacy Docker installation requires Docker Compose for migration"

  if [[ ! -f "${DATA_DIR}/panel.db" ]]; then
    volume_path="$(docker volume inspect vps-panel_panel-data --format '{{ .Mountpoint }}' 2>/dev/null || true)"
    [[ -n "$volume_path" && -f "${volume_path}/panel.db" ]] || \
      fail "cannot locate the legacy SQLite volume; the existing installation was not changed"
  fi

  log "Stopping the legacy Docker Compose deployment..."
  docker compose \
    --project-directory "${INSTALL_DIR}/deploy" \
    -f "${INSTALL_DIR}/deploy/docker-compose.yml" \
    down --remove-orphans
  legacy_docker_stopped=1
  rollback_needed=1

  if [[ ! -f "${DATA_DIR}/panel.db" ]]; then
    log "Migrating the existing SQLite data..."
    cp -a "${volume_path}/." "$DATA_DIR/"
    chown -R vps-panel:vps-panel "$DATA_DIR"
  fi
}

write_service_configuration() {
  install -d -m 0755 "$CONFIG_DIR"
  if [[ -f "$CONFIG_FILE" ]]; then
    config_existed=1
    cp -a "$CONFIG_FILE" "${temporary_dir}/environment.backup"
  fi
  if [[ -f "$SERVICE_FILE" ]]; then
    unit_existed=1
    cp -a "$SERVICE_FILE" "${temporary_dir}/vps-panel.service.backup"
  fi

  cat >"${temporary_dir}/environment" <<EOF
PANEL_DOMAIN=${requested_domain}
PANEL_LISTEN_ADDR=${listen_address}
PANEL_DATA_DIR=${DATA_DIR}
PANEL_WEB_DIR=${INSTALL_DIR}/web
EOF
  install -m 0644 "${temporary_dir}/environment" "$CONFIG_FILE"

  cat >"${temporary_dir}/vps-panel.service" <<EOF
[Unit]
Description=VPS Panel
After=network.target

[Service]
Type=simple
User=vps-panel
Group=vps-panel
EnvironmentFile=${CONFIG_FILE}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/vps-panel
Restart=on-failure
RestartSec=3s
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=${DATA_DIR}
UMask=0027

[Install]
WantedBy=multi-user.target
EOF
  install -m 0644 "${temporary_dir}/vps-panel.service" "$SERVICE_FILE"
}

ensure_caddy() {
  local distribution=""

  if command -v caddy >/dev/null 2>&1; then
    return 0
  fi

  [[ -r /etc/os-release ]] || fail "Caddy installation requires Debian or Ubuntu"
  # shellcheck disable=SC1091
  . /etc/os-release
  distribution="${ID:-}"
  case "$distribution" in
    debian | ubuntu) ;;
    *) fail "automatic Caddy installation supports Debian and Ubuntu only" ;;
  esac
  command -v apt-get >/dev/null 2>&1 || fail "apt-get is required to install Caddy"

  log "Installing native Caddy for HTTPS reverse proxy..."
  apt-get update
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https gnupg
  curl --proto '=https' --tlsv1.2 -fsSL \
    https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
    | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl --proto '=https' --tlsv1.2 -fsSL \
    https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
    -o /etc/apt/sources.list.d/caddy-stable.list
  chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  chmod o+r /etc/apt/sources.list.d/caddy-stable.list
  apt-get update
  apt-get install -y caddy
  caddy_installed=1
}

backup_caddy_configuration() {
  if [[ -f "$CADDY_FILE" ]]; then
    caddy_main_existed=1
    cp -a "$CADDY_FILE" "${temporary_dir}/Caddyfile.backup"
  fi
  if [[ -f "$CADDY_SNIPPET" ]]; then
    caddy_snippet_existed=1
    cp -a "$CADDY_SNIPPET" "${temporary_dir}/vps-panel.caddy.backup"
  fi
  caddy_changed=1
}

configure_domain_proxy() {
  ensure_caddy
  install -d -m 0755 /etc/caddy
  [[ -f "$CADDY_FILE" ]] || touch "$CADDY_FILE"
  backup_caddy_configuration

  if ! grep -Fqx "$CADDY_IMPORT" "$CADDY_FILE"; then
    printf '\n%s\n' "$CADDY_IMPORT" >>"$CADDY_FILE"
  fi

  cat >"${temporary_dir}/vps-panel.caddy" <<EOF
${requested_domain} {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080

	header {
		X-Content-Type-Options nosniff
		X-Frame-Options SAMEORIGIN
		Referrer-Policy strict-origin-when-cross-origin
	}
}
EOF
  install -m 0644 "${temporary_dir}/vps-panel.caddy" "$CADDY_SNIPPET"
  caddy validate --config "$CADDY_FILE"
  systemctl enable --now caddy.service
  systemctl reload caddy.service
}

remove_domain_proxy() {
  local filtered_caddyfile="${temporary_dir}/Caddyfile.filtered"

  if [[ ! -f "$CADDY_SNIPPET" ]] && \
    { [[ ! -f "$CADDY_FILE" ]] || ! grep -Fqx "$CADDY_IMPORT" "$CADDY_FILE"; }; then
    return 0
  fi

  backup_caddy_configuration
  rm -f -- "$CADDY_SNIPPET"
  if [[ -f "$CADDY_FILE" ]]; then
    grep -Fvx "$CADDY_IMPORT" "$CADDY_FILE" >"$filtered_caddyfile" || true
    install -m 0644 "$filtered_caddyfile" "$CADDY_FILE"
  fi
  if command -v caddy >/dev/null 2>&1; then
    caddy validate --config "$CADDY_FILE"
    if systemctl is-active --quiet caddy.service; then
      systemctl reload caddy.service
    fi
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
    *) fail "unknown argument: $1" ;;
  esac
done

if [[ ${EUID} -ne 0 ]]; then
  fail "run this installer as root, for example: curl ... | sudo bash"
fi

for command_name in curl tar uname systemctl install getent groupadd useradd; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done
[[ -d /run/systemd/system ]] || fail "systemd is not running on this VPS"

if [[ -L "$INSTALL_DIR" ]]; then
  fail "${INSTALL_DIR} must not be a symbolic link"
fi
if [[ -d "$INSTALL_DIR" && ! -f "$INSTALL_MARKER" ]]; then
  if [[ -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    fail "${INSTALL_DIR} already exists and was not created by this installer"
  fi
fi

choose_domain
detect_architecture

temporary_dir="$(mktemp -d)"
staged_dir="/opt/.vps-panel.new.$$"
backup_dir="/opt/.vps-panel.backup.$$"
trap on_exit EXIT
[[ ! -e "$staged_dir" && ! -e "$backup_dir" ]] || fail "temporary installation path already exists"

archive_name="vps-panel-linux-${architecture}.tar.gz"
archive_path="${temporary_dir}/${archive_name}"
payload_dir="${temporary_dir}/payload"
install -d -m 0755 "$payload_dir"

log "Downloading ${archive_name} from the latest GitHub Release..."
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 \
  "${RELEASE_DOWNLOAD_BASE}/${archive_name}" \
  -o "$archive_path"
curl --proto '=https' --tlsv1.2 -fL --retry 3 --retry-delay 2 \
  "$REMOTE_MANAGER" \
  -o "${temporary_dir}/vp"
tar -xzf "$archive_path" -C "$payload_dir"
[[ -f "${payload_dir}/vps-panel" ]] || fail "release archive does not contain vps-panel"
[[ -f "${payload_dir}/web/index.html" ]] || fail "release archive does not contain web/index.html"

install -d -m 0755 "$staged_dir"
install -m 0755 "${payload_dir}/vps-panel" "${staged_dir}/vps-panel"
cp -a "${payload_dir}/web" "${staged_dir}/web"
touch "${staged_dir}/.vps-panel-install"
chown -R root:root "$staged_dir"
chmod -R a+rX "$staged_dir"

ensure_service_user
migrate_legacy_docker_data
write_service_configuration

if [[ -d "$INSTALL_DIR" ]]; then
  had_existing_install=1
fi
rollback_needed=1
systemctl stop vps-panel.service >/dev/null 2>&1 || true
if [[ "$had_existing_install" -eq 1 ]]; then
  mv "$INSTALL_DIR" "$backup_dir"
fi
mv "$staged_dir" "$INSTALL_DIR"
install_swapped=1

systemctl daemon-reload
systemctl enable --now vps-panel.service

log "Waiting for Panel health check..."
if ! wait_for_panel; then
  systemctl status vps-panel.service --no-pager >&2 || true
  journalctl -u vps-panel.service -n 50 --no-pager >&2 || true
  fail "Panel did not become healthy within 60 seconds"
fi

if [[ "$requested_domain" == ":80" ]]; then
  remove_domain_proxy
else
  configure_domain_proxy
  log "Waiting for HTTPS certificate and domain access..."
  if ! wait_for_https; then
    journalctl -u caddy.service -n 80 --no-pager >&2 || true
    fail "HTTPS is not ready; verify DNS and that ports 80/443 are open"
  fi
fi

install -d -m 0755 /usr/local/bin
install -m 0755 "${temporary_dir}/vp" "$COMMAND_PATH"
rollback_needed=0

log "Management command installed: vp"
if [[ "$legacy_docker_install" -eq 1 ]]; then
  log "Legacy SQLite data was migrated; old Docker volumes were preserved"
fi
if [[ "$requested_domain" == ":80" ]]; then
  log "Installation complete. Open http://YOUR_VPS_IP:8080"
else
  log "Installation complete. Open https://${requested_domain}"
fi
