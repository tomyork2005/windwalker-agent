#!/usr/bin/env bash
# One-command bootstrap for skywalker-agent on a fresh Linux VPS
# (Ubuntu 22.04 / 24.04 LTS, Debian 12). Run from the repo root.
#
# Required env:
#   XRAY_DOMAIN         REALITY serverNames[0] — fake SNI domain
#   CONTROL_PLANE_ADDR  host:port of the control plane
#   AGENT_ID            stable identity of this node
#
# Optional env (with sensible defaults):
#   AGENT_PUBLIC_HOST   public IP/domain of this node; auto-detected via api.ipify.org if empty
#   REGION              ISO alpha-2 country code (e.g. "lv"); empty by default
#   AGENT_VERSION       arbitrary build tag, empty by default
#   GO_VERSION          1.25.5; only consulted if Go is missing or below 1.25
#   GOOSE_VERSION       latest; pin a tag (e.g. v3.22.1) for reproducibility
#   XRAY_DEST           ${XRAY_DOMAIN}:443
#   XRAY_PORT           443
#   XRAY_API_ADDR       127.0.0.1:10085
#   XRAY_INBOUND_TAG    vless-in
#   XRAY_VLESS_FLOW     xtls-rprx-vision
#   XRAY_SERVICE_NAME   xray
#   XRAY_PRIVATE_KEY    auto-generated via `xray x25519` if empty
#   XRAY_PUBLIC_KEY     auto-generated alongside private
#   XRAY_SHORT_ID       auto-generated via `openssl rand -hex 8` if empty
#
# Usage:
#   sudo XRAY_DOMAIN=cdn.example.com \
#        CONTROL_PLANE_ADDR=control.example.com:50051 \
#        AGENT_ID=LV-1 \
#        ./deploy.sh

set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_DIR="${REPO_ROOT}/deploy"

log() { echo "[deploy] $*"; }
die() { echo "[deploy][error] $*" >&2; exit 1; }

usage() { sed -n '2,/^set -Eeuo/p' "${BASH_SOURCE[0]}" | sed 's/^# //;s/^#//' | sed '$d'; }
[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && usage && exit 0

# ---------- preflight ----------

[[ "${EUID}" -eq 0 ]] || die "run as root (use sudo)"

[[ -f "${REPO_ROOT}/go.mod"                          ]] || die "go.mod not found — run from repo root"
[[ -f "${REPO_ROOT}/cmd/main.go"                     ]] || die "cmd/main.go not found"
[[ -f "${REPO_ROOT}/migrations/00001_init.sql"       ]] || die "migrations/00001_init.sql not found"
[[ -f "${REPO_ROOT}/config/config.yaml"              ]] || die "config/config.yaml not found"
[[ -f "${SCRIPT_DIR}/skywalker-agent.service"        ]] || die "deploy/skywalker-agent.service not found"
[[ -f "${SCRIPT_DIR}/xray-config.template.json"      ]] || die "deploy/xray-config.template.json not found"

XRAY_DOMAIN="${XRAY_DOMAIN:-}";              [[ -n "${XRAY_DOMAIN}"        ]] || die "XRAY_DOMAIN is required"
CONTROL_PLANE_ADDR="${CONTROL_PLANE_ADDR:-}";[[ -n "${CONTROL_PLANE_ADDR}" ]] || die "CONTROL_PLANE_ADDR is required"
AGENT_ID="${AGENT_ID:-}";                    [[ -n "${AGENT_ID}"           ]] || die "AGENT_ID is required"

REGION="${REGION:-}"
AGENT_VERSION="${AGENT_VERSION:-}"

GO_VERSION="${GO_VERSION:-1.25.5}"
GOOSE_VERSION="${GOOSE_VERSION:-latest}"

XRAY_DEST="${XRAY_DEST:-${XRAY_DOMAIN}:443}"
XRAY_PORT="${XRAY_PORT:-443}"
XRAY_API_ADDR="${XRAY_API_ADDR:-127.0.0.1:10085}"
XRAY_INBOUND_TAG="${XRAY_INBOUND_TAG:-vless-in}"
XRAY_VLESS_FLOW="${XRAY_VLESS_FLOW:-xtls-rprx-vision}"
XRAY_SERVICE_NAME="${XRAY_SERVICE_NAME:-xray}"

AGENT_PUBLIC_HOST="${AGENT_PUBLIC_HOST:-}"

AGENT_BIN_PATH="/usr/local/bin/skywalker-agent"
AGENT_CONFIG_DIR="/etc/skywalker-agent"
AGENT_CONFIG_PATH="${AGENT_CONFIG_DIR}/config.yaml"
AGENT_DATA_DIR="/var/lib/skywalker-agent"
AGENT_DB_PATH="${AGENT_DATA_DIR}/agent.db"

XRAY_CONFIG_DIR="/usr/local/etc/xray"
XRAY_CONFIG_PATH="${XRAY_CONFIG_DIR}/config.json"

GO_BIN="/usr/local/go/bin/go"
GOOSE_BIN="/usr/local/bin/goose"

escape_sed() { printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "unsupported CPU arch: $(uname -m)" ;;
  esac
}

# ---------- steps ----------

install_base_packages() {
  log "installing base packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y --no-install-recommends \
    ca-certificates curl openssl tar gzip
}

go_version_ok() {
  command -v go >/dev/null 2>&1 || return 1
  # Need Go >= 1.25 (go.mod requires it).
  go version | awk '{print $3}' | sed 's/^go//' | awk -F. '{exit !($1 > 1 || ($1 == 1 && $2 >= 25))}'
}

install_go_if_needed() {
  if go_version_ok; then
    log "Go OK: $(go version)"
    return
  fi

  local arch tarball
  arch="$(detect_arch)"
  tarball="go${GO_VERSION}.linux-${arch}.tar.gz"

  log "installing Go ${GO_VERSION} for ${arch}"
  curl -fsSL "https://go.dev/dl/${tarball}" -o "/tmp/${tarball}"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "/tmp/${tarball}"
  rm -f "/tmp/${tarball}"
  ln -sf /usr/local/go/bin/go    /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  log "Go installed: $(go version)"
}

install_xray() {
  if command -v xray >/dev/null 2>&1; then
    log "xray already installed: $(xray version | head -n 1)"
    return
  fi
  log "installing xray via official installer"
  bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install
  command -v xray >/dev/null 2>&1 || die "xray installation failed"
  log "xray installed: $(xray version | head -n 1)"
}

install_goose() {
  if [[ -x "${GOOSE_BIN}" ]]; then
    log "goose already installed: $("${GOOSE_BIN}" -version 2>&1 | head -n 1)"
    return
  fi
  log "installing goose ${GOOSE_VERSION} (uses modernc.org/sqlite, no CGO)"
  GOBIN=/usr/local/bin "${GO_BIN}" install \
    "github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}"
  [[ -x "${GOOSE_BIN}" ]] || die "goose install failed"
  log "goose installed: $("${GOOSE_BIN}" -version 2>&1 | head -n 1)"
}

generate_xray_keys_if_needed() {
  if [[ -n "${XRAY_PRIVATE_KEY:-}" && -n "${XRAY_PUBLIC_KEY:-}" ]]; then
    log "using provided REALITY keypair"
  else
    log "generating REALITY keypair via xray x25519"
    local out
    out="$(xray x25519)"
    # Match both old ("Private key:" / "Public key:") and new
    # ("PrivateKey:" / "Password (PublicKey):") xray output formats.
    XRAY_PRIVATE_KEY="$(printf '%s\n' "${out}" | awk -F': ' '/^[[:space:]]*(Private ?[Kk]ey|PrivateKey)/ {print $NF; exit}')"
    XRAY_PUBLIC_KEY="$(printf '%s\n' "${out}"  | awk -F': ' '/^[[:space:]]*(Public ?[Kk]ey|Password([[:space:]]*\(PublicKey\))?)/ {print $NF; exit}')"
  fi
  [[ -n "${XRAY_PRIVATE_KEY:-}" ]] || die "failed to obtain XRAY_PRIVATE_KEY"
  [[ -n "${XRAY_PUBLIC_KEY:-}"  ]] || die "failed to obtain XRAY_PUBLIC_KEY"

  if [[ -z "${XRAY_SHORT_ID:-}" ]]; then
    XRAY_SHORT_ID="$(openssl rand -hex 8)"
  fi
  log "REALITY pubkey  : ${XRAY_PUBLIC_KEY}"
  log "REALITY shortId : ${XRAY_SHORT_ID}"
}

resolve_public_host() {
  if [[ -n "${AGENT_PUBLIC_HOST}" ]]; then
    log "agent public host: ${AGENT_PUBLIC_HOST}"
    return
  fi
  log "AGENT_PUBLIC_HOST not set — auto-detecting via api.ipify.org"
  AGENT_PUBLIC_HOST="$(curl -fsS --max-time 5 https://api.ipify.org || true)"
  [[ -n "${AGENT_PUBLIC_HOST}" ]] || die "auto-detection of public IP failed; set AGENT_PUBLIC_HOST"
  log "agent public host: ${AGENT_PUBLIC_HOST} (auto)"
}

write_agent_config() {
  log "writing agent config to ${AGENT_CONFIG_PATH}"
  install -d -m 0755 "${AGENT_CONFIG_DIR}"
  install -d -m 0755 "${AGENT_DATA_DIR}"

  # Patch the repo template's empty fields. All other tunables (timeouts,
  # worker intervals, log level) stay as the repo defaults.
  sed \
    -e "s|^\([[:space:]]*agent_id:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${AGENT_ID}")\"|" \
    -e "s|^\([[:space:]]*public_host:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${AGENT_PUBLIC_HOST}")\"|" \
    -e "s|^\([[:space:]]*sni:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${XRAY_DOMAIN}")\"|" \
    -e "s|^\([[:space:]]*public_key:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${XRAY_PUBLIC_KEY}")\"|" \
    -e "s|^\([[:space:]]*short_id:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${XRAY_SHORT_ID}")\"|" \
    -e "s|^\([[:space:]]*address:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${CONTROL_PLANE_ADDR}")\"|" \
    -e "s|^\([[:space:]]*region:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${REGION}")\"|" \
    -e "s|^\([[:space:]]*version:\)[[:space:]]*\"\"|\1 \"$(escape_sed "${AGENT_VERSION}")\"|" \
    "${REPO_ROOT}/config/config.yaml" > "${AGENT_CONFIG_PATH}"

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
  log "building skywalker-agent"
  cd "${REPO_ROOT}"
  "${GO_BIN}" mod download
  "${GO_BIN}" build -trimpath -ldflags="-s -w" -o "${AGENT_BIN_PATH}" ./cmd
  chmod 0755 "${AGENT_BIN_PATH}"
  log "agent binary: ${AGENT_BIN_PATH}"
}

apply_migrations() {
  log "applying migrations to ${AGENT_DB_PATH}"
  cd "${REPO_ROOT}"
  "${GOOSE_BIN}" -dir migrations sqlite3 "${AGENT_DB_PATH}" up
}

install_systemd_unit() {
  log "installing systemd unit"
  install -m 0644 "${SCRIPT_DIR}/skywalker-agent.service" /etc/systemd/system/skywalker-agent.service
  systemctl daemon-reload
  systemctl enable "${XRAY_SERVICE_NAME}.service"
  systemctl enable skywalker-agent.service
}

restart_services() {
  log "restarting xray and skywalker-agent"
  systemctl restart "${XRAY_SERVICE_NAME}.service"
  systemctl restart skywalker-agent.service
}

print_summary() {
  cat <<EOF

============================================================
skywalker-agent deployed

agent binary:    ${AGENT_BIN_PATH}
agent config:    ${AGENT_CONFIG_PATH}
agent db:        ${AGENT_DB_PATH}
xray config:     ${XRAY_CONFIG_PATH}

agent_id:        ${AGENT_ID}
control plane:   ${CONTROL_PLANE_ADDR}
region:          ${REGION:-<unset>}
public host:     ${AGENT_PUBLIC_HOST}

REALITY domain:  ${XRAY_DOMAIN}
REALITY dest:    ${XRAY_DEST}
REALITY pubkey:  ${XRAY_PUBLIC_KEY}
REALITY shortId: ${XRAY_SHORT_ID}

Status:
  systemctl status ${XRAY_SERVICE_NAME} --no-pager
  systemctl status skywalker-agent --no-pager

Logs:
  journalctl -u ${XRAY_SERVICE_NAME} -f
  journalctl -u skywalker-agent -f
============================================================

EOF
}

# ---------- run ----------

install_base_packages
install_go_if_needed
install_xray
install_goose
resolve_public_host
generate_xray_keys_if_needed
write_agent_config
write_xray_config
build_agent
apply_migrations
install_systemd_unit
restart_services
print_summary
