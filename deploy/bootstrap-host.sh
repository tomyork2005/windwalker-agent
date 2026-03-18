#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

log() {
  echo "[bootstrap] $*"
}

die() {
  echo "[bootstrap][error] $*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage:
  sudo CONTROL_PLANE_ADDR=<host:port> XRAY_DOMAIN=<domain> [other envs] ./deploy/bootstrap-host.sh

Required env:
  CONTROL_PLANE_ADDR   gRPC address of control-plane reachable from this server
  XRAY_DOMAIN          domain used in REALITY serverNames

Optional env:
  REGION               default: FIN
  AGENT_ENV            default: prod
  AGENT_VERSION        default: 1.0.0

  GO_VERSION           if Go is absent, script installs this version from go.dev
  FORCE_INSTALL_GO     1/0, default: 0

  XRAY_DEST            default: ${XRAY_DOMAIN}:443
  XRAY_PORT            default: 443
  XRAY_API_ADDR        default: 127.0.0.1:10085
  XRAY_PROTOCOL        default: vless
  XRAY_INBOUND_TAG     default: vless-in
  XRAY_VLESS_FLOW      default: xtls-rprx-vision
  XRAY_SERVICE_NAME    default: xray

  XRAY_PRIVATE_KEY     optional; if empty, generated automatically
  XRAY_PUBLIC_KEY      optional; if empty, generated automatically with private key
  XRAY_SHORT_ID        optional; if empty, generated automatically

Examples:
  sudo CONTROL_PLANE_ADDR=1.2.3.4:9000 XRAY_DOMAIN=cdn.example.com ./deploy/bootstrap-host.sh

  sudo CONTROL_PLANE_ADDR=control.example.com:9000 \
       XRAY_DOMAIN=cdn.example.com \
       REGION=NLD \
       GO_VERSION=1.25.8 \
       ./deploy/bootstrap-host.sh
EOF
}

[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && usage && exit 0

[[ "${EUID}" -eq 0 ]] || die "run as root"

[[ -f "${REPO_ROOT}/go.mod" ]] || die "go.mod not found, script must live in deploy/ inside repo"
[[ -f "${REPO_ROOT}/cmd/agent/main.go" ]] || die "cmd/agent/main.go not found"
[[ -f "${SCRIPT_DIR}/skywalker-agent.service" ]] || die "deploy/skywalker-agent.service not found"
[[ -f "${SCRIPT_DIR}/xray-config.template.json" ]] || die "deploy/xray-config.template.json not found"

CONTROL_PLANE_ADDR="${CONTROL_PLANE_ADDR:-}"
XRAY_DOMAIN="${XRAY_DOMAIN:-}"

[[ -n "${CONTROL_PLANE_ADDR}" ]] || die "CONTROL_PLANE_ADDR is required"
[[ -n "${XRAY_DOMAIN}" ]] || die "XRAY_DOMAIN is required"

REGION="${REGION:-FIN}"
AGENT_ENV="${AGENT_ENV:-prod}"
AGENT_VERSION="${AGENT_VERSION:-1.0.0}"

GO_VERSION="${GO_VERSION:-}"
FORCE_INSTALL_GO="${FORCE_INSTALL_GO:-0}"

XRAY_DEST="${XRAY_DEST:-${XRAY_DOMAIN}:443}"
XRAY_PORT="${XRAY_PORT:-443}"
XRAY_API_ADDR="${XRAY_API_ADDR:-127.0.0.1:10085}"
XRAY_PROTOCOL="${XRAY_PROTOCOL:-vless}"
XRAY_INBOUND_TAG="${XRAY_INBOUND_TAG:-vless-in}"
XRAY_VLESS_FLOW="${XRAY_VLESS_FLOW:-xtls-rprx-vision}"
XRAY_SERVICE_NAME="${XRAY_SERVICE_NAME:-xray}"

AGENT_BIN_PATH="/usr/local/bin/skywalker-agent"
AGENT_CONFIG_DIR="/etc/skywalker-agent"
AGENT_CONFIG_PATH="${AGENT_CONFIG_DIR}/config.yaml"
AGENT_DATA_DIR="/var/lib/skywalker-agent"

XRAY_CONFIG_DIR="/usr/local/etc/xray"
XRAY_CONFIG_PATH="${XRAY_CONFIG_DIR}/config.json"

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

detect_go_arch() {
  local arch
  arch="$(uname -m)"
  case "${arch}" in
    x86_64|amd64)
      echo "amd64"
      ;;
    aarch64|arm64)
      echo "arm64"
      ;;
    *)
      die "unsupported CPU architecture: ${arch}"
      ;;
  esac
}

install_base_packages() {
  log "installing base packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y \
    ca-certificates \
    curl \
    git \
    openssl \
    sed \
    grep \
    coreutils \
    tar \
    gzip
}

install_go_if_needed() {
  local go_arch
  go_arch="$(detect_go_arch)"

  if command -v go >/dev/null 2>&1 && [[ "${FORCE_INSTALL_GO}" != "1" ]]; then
    log "Go already installed: $(go version)"
    return
  fi

  [[ -n "${GO_VERSION}" ]] || die "Go is not installed. Set GO_VERSION, e.g. GO_VERSION=1.25.8"

  log "installing Go ${GO_VERSION} for ${go_arch}"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${go_arch}.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  export PATH="/usr/local/go/bin:${PATH}"

  log "Go installed: $(go version)"
}

install_xray() {
  if command -v xray >/dev/null 2>&1; then
    log "xray already installed: $(xray version | head -n 1)"
    return
  fi

  log "installing xray"
  bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install
  command -v xray >/dev/null 2>&1 || die "xray installation failed"
  log "xray installed: $(xray version | head -n 1)"
}

generate_xray_keys_if_needed() {
  if [[ -n "${XRAY_PRIVATE_KEY:-}" && -n "${XRAY_PUBLIC_KEY:-}" ]]; then
    log "using provided REALITY keys"
  else
    log "generating REALITY keypair"
    local key_output
    key_output="$(xray x25519)"
    XRAY_PRIVATE_KEY="$(printf '%s\n' "${key_output}" | awk -F': ' '/Private key:/ {print $2}')"
    XRAY_PUBLIC_KEY="$(printf '%s\n' "${key_output}" | awk -F': ' '/Public key:/ {print $2}')"
  fi

  [[ -n "${XRAY_PRIVATE_KEY:-}" ]] || die "failed to get XRAY_PRIVATE_KEY"
  [[ -n "${XRAY_PUBLIC_KEY:-}" ]] || die "failed to get XRAY_PUBLIC_KEY"

  if [[ -z "${XRAY_SHORT_ID:-}" ]]; then
    XRAY_SHORT_ID="$(openssl rand -hex 8)"
  fi
}

write_agent_config() {
  log "writing agent config to ${AGENT_CONFIG_PATH}"
  install -d -m 0755 "${AGENT_CONFIG_DIR}"
  install -d -m 0755 "${AGENT_DATA_DIR}"

  cat > "${AGENT_CONFIG_PATH}" <<EOF
env: "${AGENT_ENV}"

transport_grpc:
  address: "${CONTROL_PLANE_ADDR}"
  agent_id: ""
  instance_id: ""
  region: "${REGION}"
  version: "${AGENT_VERSION}"
  driver_types: ["xray"]

  heartbeat_period: 20s
  send_queue_size: 128
  reconnect_min: 500ms
  reconnect_max: 10s
  dial_timeout: 5s

driver_xray:
  service_name: "${XRAY_SERVICE_NAME}"
  api_addr: "${XRAY_API_ADDR}"
  inbound_tag: "${XRAY_INBOUND_TAG}"
  protocol: "${XRAY_PROTOCOL}"
  vless_flow: "${XRAY_VLESS_FLOW}"
  op_timeout: 3s

storage_sqlite:
  path: "${AGENT_DATA_DIR}/agent.db"
  busy_timeout: 5s
  wal: true
  synchronous_full: false
  foreign_keys: true
EOF

  chmod 0644 "${AGENT_CONFIG_PATH}"
}

write_xray_config() {
  log "writing xray config to ${XRAY_CONFIG_PATH}"
  install -d -m 0755 "${XRAY_CONFIG_DIR}"

  sed \
    -e "s|__XRAY_API_ADDR__|$(escape_sed "${XRAY_API_ADDR}")|g" \
    -e "s|__XRAY_PORT__|$(escape_sed "${XRAY_PORT}")|g" \
    -e "s|__XRAY_INBOUND_TAG__|$(escape_sed "${XRAY_INBOUND_TAG}")|g" \
    -e "s|__XRAY_DOMAIN__|$(escape_sed "${XRAY_DOMAIN}")|g" \
    -e "s|__XRAY_DEST__|$(escape_sed "${XRAY_DEST}")|g" \
    -e "s|__XRAY_PRIVATE_KEY__|$(escape_sed "${XRAY_PRIVATE_KEY}")|g" \
    -e "s|__XRAY_SHORT_ID__|$(escape_sed "${XRAY_SHORT_ID}")|g" \
    "${SCRIPT_DIR}/xray-config.template.json" > "${XRAY_CONFIG_PATH}"

  xray run -test -config "${XRAY_CONFIG_PATH}"
}

build_agent() {
  log "building skywalker-agent from current repo checkout"
  export PATH="/usr/local/go/bin:${PATH}"
  cd "${REPO_ROOT}"
  go build -trimpath -ldflags="-s -w" -o "${AGENT_BIN_PATH}" ./cmd/agent
  chmod 0755 "${AGENT_BIN_PATH}"
}

install_systemd_unit() {
  log "installing systemd unit"
  install -m 0644 "${SCRIPT_DIR}/skywalker-agent.service" /etc/systemd/system/skywalker-agent.service
  systemctl daemon-reload
  systemctl enable "${XRAY_SERVICE_NAME}.service"
  systemctl enable skywalker-agent.service
}

restart_services() {
  log "restarting xray"
  systemctl restart "${XRAY_SERVICE_NAME}.service"

  log "restarting skywalker-agent"
  systemctl restart skywalker-agent.service
}

print_summary() {
  cat <<EOF

============================================================
skywalker-agent deployed successfully

agent binary:   ${AGENT_BIN_PATH}
agent config:   ${AGENT_CONFIG_PATH}
agent data dir: ${AGENT_DATA_DIR}
xray config:    ${XRAY_CONFIG_PATH}

control-plane:  ${CONTROL_PLANE_ADDR}
region:         ${REGION}

REALITY domain: ${XRAY_DOMAIN}
REALITY dest:   ${XRAY_DEST}
REALITY pubkey: ${XRAY_PUBLIC_KEY}
REALITY shortId:${XRAY_SHORT_ID}

Check status:
  systemctl status ${XRAY_SERVICE_NAME} --no-pager
  systemctl status skywalker-agent --no-pager

Tail logs:
  journalctl -u ${XRAY_SERVICE_NAME} -f
  journalctl -u skywalker-agent -f
============================================================

EOF
}

install_base_packages
install_go_if_needed
install_xray
generate_xray_keys_if_needed
write_agent_config
write_xray_config
build_agent
install_systemd_unit
restart_services
print_summary