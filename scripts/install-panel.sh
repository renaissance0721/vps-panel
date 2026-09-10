#!/usr/bin/env bash

set -Eeuo pipefail

readonly REPOSITORY_ARCHIVE="https://github.com/renaissance0721/vps-panel/archive/refs/heads/main.tar.gz"
readonly INSTALL_DIR="/opt/vps-panel"
readonly INSTALL_MARKER="${INSTALL_DIR}/.vps-panel-install"
readonly ENVIRONMENT_FILE="${INSTALL_DIR}/deploy/.env"

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

choose_domain() {
  local existing_domain=""
  local entered_domain=""

  if [[ -f "$ENVIRONMENT_FILE" ]]; then
    existing_domain="$(sed -n 's/^PANEL_DOMAIN=//p' "$ENVIRONMENT_FILE" | tail -n 1)"
  fi

  if [[ -z "$requested_domain" && -t 1 && -r /dev/tty ]]; then
    if [[ -n "$existing_domain" ]]; then
      printf '[vps-panel] Panel domain [%s]: ' "$existing_domain" >/dev/tty
    else
      printf '[vps-panel] Panel domain (leave empty for IP/HTTP): ' >/dev/tty
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
}

start_docker() {
  if command -v systemctl >/dev/null 2>&1; then
    systemctl enable --now docker
  elif command -v service >/dev/null 2>&1; then
    service docker start
  else
    fail "cannot start Docker: no supported service manager was found"
  fi
}

install_docker() {
  [[ -r /etc/os-release ]] || fail "cannot detect the operating system"

  # shellcheck disable=SC1091
  . /etc/os-release

  local distribution="${ID:-}"
  local codename="${VERSION_CODENAME:-}"
  if [[ "$distribution" == "ubuntu" ]]; then
    codename="${UBUNTU_CODENAME:-$codename}"
  fi

  case "$distribution" in
    debian | ubuntu) ;;
    *) fail "automatic Docker installation supports Debian and Ubuntu only" ;;
  esac

  [[ -n "$codename" ]] || fail "cannot determine the ${distribution} release codename"
  command -v apt-get >/dev/null 2>&1 || fail "apt-get is required to install Docker"
  command -v dpkg >/dev/null 2>&1 || fail "dpkg is required to install Docker"

  local architecture
  architecture="$(dpkg --print-architecture)"

  log "Docker was not found; installing Docker Engine and Compose..."
  apt-get update
  apt-get install -y ca-certificates curl
  install -d -m 0755 /etc/apt/keyrings
  curl --proto '=https' --tlsv1.2 -fsSL \
    "https://download.docker.com/linux/${distribution}/gpg" \
    -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/${distribution}
Suites: ${codename}
Components: stable
Architectures: ${architecture}
Signed-By: /etc/apt/keyrings/docker.asc
EOF

  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  start_docker
}

wait_for_panel() {
  for _ in {1..30}; do
    if docker compose exec -T panel /app/vps-panel healthcheck >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_for_https() {
  local domain="$1"
  local response=""

  for _ in {1..30}; do
    if response="$(curl --proto '=https' --tlsv1.2 -fsS \
      --connect-timeout 2 --max-time 4 \
      "https://${domain}/api/health" 2>/dev/null)"; then
      if [[ "$response" == *'"status":"ok"'* ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
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

for command_name in curl tar; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done

choose_domain

if ! command -v docker >/dev/null 2>&1; then
  install_docker
fi

docker compose version >/dev/null 2>&1 || fail "Docker is installed, but the Compose plugin is missing"
if ! docker info >/dev/null 2>&1; then
  log "Starting Docker daemon..."
  start_docker
fi
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
install -d -m 0755 /usr/local/bin
install -m 0755 "${INSTALL_DIR}/scripts/vp" /usr/local/bin/vp

printf 'PANEL_DOMAIN=%s\n' "$requested_domain" >"$ENVIRONMENT_FILE"
chmod 0600 "$ENVIRONMENT_FILE"

cd "${INSTALL_DIR}/deploy"
log "Building and starting containers..."
docker compose up -d --build

log "Waiting for Panel health check..."
if ! wait_for_panel; then
  docker compose ps >&2
  docker compose logs --tail=50 panel caddy >&2
  fail "Panel did not become healthy within 60 seconds"
fi

log "Management command installed: vp"

if [[ "$requested_domain" == ":80" ]]; then
  log "Installation complete. Open http://YOUR_VPS_IP"
  exit 0
fi

log "Waiting for HTTPS certificate and domain access..."
if ! wait_for_https "$requested_domain"; then
  docker compose logs --tail=80 caddy >&2
  fail "HTTPS is not ready; verify the domain DNS records and that ports 80/443 are open"
fi

log "Installation complete. Open https://${requested_domain}"
