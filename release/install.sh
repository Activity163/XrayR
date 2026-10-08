#!/usr/bin/env bash
#
# XrayR one-click installer for Debian.
#
# Installs XrayR, its configuration directory, the geoip/geosite rule data and a
# systemd unit, then (optionally) starts the service.
#
#   bash install.sh                          # build from source, install, enable
#   bash install.sh --source-dir /root/XrayR # build from an existing checkout
#   bash install.sh --release                # use a prebuilt release archive
#   bash install.sh --init                   # run `XrayR config init` afterwards
#   bash install.sh --uninstall              # remove the binary and the unit
#   bash install.sh --uninstall --purge      # ...and the configuration directory
#
# Tested on Debian 11 (bullseye), 12 (bookworm) and 13 (trixie) on amd64 and arm64.
set -euo pipefail

REPO_SLUG="${XRAYR_REPO:-Activity163/XrayR}"
REF="${XRAYR_REF:-master}"
RAW_BASE="${XRAYR_RAW_BASE:-https://raw.githubusercontent.com/${REPO_SLUG}/${REF}}"
RELEASE_BASE="${XRAYR_RELEASE_BASE:-https://github.com/${REPO_SLUG}/releases}"
GO_VERSION="${XRAYR_GO_VERSION:-1.25.3}"
GO_ROOT="${XRAYR_GO_ROOT:-/usr/local/go}"

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/XrayR"
SERVICE_PATH="/etc/systemd/system/XrayR.service"
SERVICE_NAME="XrayR"

MODE="auto"          # auto | release | source
RELEASE_TAG="latest"
SOURCE_DIR=""
RUN_INIT=0
SKIP_RULES=0
SKIP_SERVICE=0
START_SERVICE=1
DO_UNINSTALL=0
PURGE=0

WORK_DIR=""

# ---------------------------------------------------------------- output helpers

if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
    C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'
    C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BLUE=$'\033[34m'
else
    C_RESET=""; C_BOLD=""; C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""
fi

log()   { printf '%s==>%s %s\n' "$C_BLUE" "$C_RESET" "$*"; }
ok()    { printf '%s  ok%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn()  { printf '%swarn%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
die()   { printf '%serror%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; exit 1; }

cleanup() {
    if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
        rm -rf "$WORK_DIR"
    fi
}
trap cleanup EXIT

usage() {
    cat <<'EOF'
XrayR one-click installer for Debian.

Installs XrayR, its configuration directory, the geoip/geosite rule data and a
systemd unit, then (optionally) starts the service.

  bash install.sh                          # build from source, install, enable
  bash install.sh --source-dir /root/XrayR # build from an existing checkout
  bash install.sh --release                # use a prebuilt release archive
  bash install.sh --init                   # run `XrayR config init` afterwards
  bash install.sh --uninstall              # remove the binary and the unit
  bash install.sh --uninstall --purge      # ...and the configuration directory

Tested on Debian 11 (bullseye), 12 (bookworm) and 13 (trixie) on amd64 and arm64.

Options:
  --release [TAG]     Install a prebuilt archive from GitHub releases instead of
                      building from source. TAG defaults to "latest".
  --build             Always build from source (default when no release exists).
  --source-dir DIR    Build from an existing checkout instead of cloning.
  --ref REF           Git ref to clone and build (default: master).
  --go-version VER    Go toolchain to install when missing (default: 1.25.3).
  --install-dir DIR   Where to put the binary (default: /usr/local/bin).
  --config-dir DIR    Where config and rule data live (default: /etc/XrayR).
  --skip-rules        Do not download geoip.dat / geosite.dat.
  --skip-service      Do not install the systemd unit.
  --no-start          Install the unit but do not enable or start it.
  --init              Run `XrayR config init` after installing.
  --uninstall         Remove the binary and the systemd unit.
  --purge             With --uninstall, also delete the configuration directory.
  -h, --help          Show this help.
EOF
}

# ------------------------------------------------------------------- prerequisites

require_root() {
    [ "$(id -u)" -eq 0 ] || die "run this script as root (sudo bash $0 ...)"
}

detect_platform() {
    [ -f /etc/os-release ] || die "cannot find /etc/os-release; this installer targets Debian"
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
        debian|ubuntu|raspbian) ;;
        *) warn "detected '${ID:-unknown}', not Debian; continuing anyway" ;;
    esac
    log "platform: ${PRETTY_NAME:-unknown}"

    case "$(uname -m)" in
        x86_64|amd64)   GO_ARCH="amd64"; ASSET="linux-64" ;;
        aarch64|arm64)  GO_ARCH="arm64"; ASSET="linux-arm64-v8a" ;;
        armv7l|armv7)   GO_ARCH="armv6l"; ASSET="linux-arm32-v7a" ;;
        armv6l|armv6)   GO_ARCH="armv6l"; ASSET="linux-arm32-v6" ;;
        i386|i686)      GO_ARCH="386";   ASSET="linux-32" ;;
        riscv64)        GO_ARCH="riscv64"; ASSET="linux-riscv64" ;;
        s390x)          GO_ARCH="s390x"; ASSET="linux-s390x" ;;
        ppc64le)        GO_ARCH="ppc64le"; ASSET="linux-ppc64le" ;;
        *) die "unsupported architecture $(uname -m)" ;;
    esac
    ok "architecture $(uname -m) (Go ${GO_ARCH}, release asset ${ASSET})"

    if [ -d /run/systemd/system ]; then
        HAS_SYSTEMD=1
    else
        HAS_SYSTEMD=0
        warn "systemd is not running; the unit will be written but cannot be enabled"
    fi
}

apt_install() {
    local missing=() pkg
    for pkg in "$@"; do
        dpkg -s "$pkg" >/dev/null 2>&1 || missing+=("$pkg")
    done
    [ ${#missing[@]} -eq 0 ] && return 0
    command -v apt-get >/dev/null 2>&1 || die "missing packages (${missing[*]}) and apt-get is unavailable"
    log "installing packages: ${missing[*]}"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends "${missing[@]}"
}

# ------------------------------------------------------------------ go toolchain

go_is_usable() {
    local candidate="$1"
    [ -x "$candidate" ] || return 1
    "$candidate" version 2>/dev/null | awk '{print $3}' | grep -q "^go${GO_VERSION}$"
}

ensure_go() {
    if go_is_usable "${GO_ROOT}/bin/go"; then
        export PATH="${GO_ROOT}/bin:${PATH}"
        ok "using existing $(go version)"
        return 0
    fi
    if command -v go >/dev/null 2>&1 && go_is_usable "$(command -v go)"; then
        ok "using existing $(go version)"
        return 0
    fi

    apt_install curl ca-certificates tar
    local tarball="${WORK_DIR}/go.tar.gz"
    local url="https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    log "installing Go ${GO_VERSION} from ${url}"
    curl -fsSL --retry 3 --retry-delay 2 -o "$tarball" "$url" ||
        die "failed to download Go ${GO_VERSION}; set --go-version to a version published for ${GO_ARCH}"

    rm -rf "$GO_ROOT"
    mkdir -p "$(dirname "$GO_ROOT")"
    tar -C "$(dirname "$GO_ROOT")" -xzf "$tarball"

    cat >/etc/profile.d/go.sh <<EOF
export GOROOT=${GO_ROOT}
export GOPATH=/root/go
export PATH=\$PATH:${GO_ROOT}/bin:/root/go/bin
export GOPROXY=https://goproxy.cn,direct
EOF
    chmod 0644 /etc/profile.d/go.sh
    export PATH="${GO_ROOT}/bin:${PATH}"
    ok "installed $(go version)"
}

# ------------------------------------------------------------------- acquisition

fetch_repo_file() {
    # Copy a file from a local checkout when the script runs inside one, otherwise
    # download it from the repository at $REF.
    local relative="$1" destination="$2"
    if [ -n "$LOCAL_ROOT" ] && [ -f "${LOCAL_ROOT}/${relative}" ]; then
        cp "${LOCAL_ROOT}/${relative}" "$destination"
    else
        curl -fsSL --retry 3 --retry-delay 2 -o "$destination" "${RAW_BASE}/${relative}"
    fi
}

release_asset_url() {
    local tag="$1"
    if [ "$tag" = "latest" ]; then
        printf '%s/latest/download/XrayR-%s.zip' "$RELEASE_BASE" "$ASSET"
    else
        printf '%s/download/%s/XrayR-%s.zip' "$RELEASE_BASE" "$tag" "$ASSET"
    fi
}

release_exists() {
    # A missing release is the normal case before the first tag, so probe quietly.
    curl -fsSLI -o /dev/null --max-time 20 "$(release_asset_url "$RELEASE_TAG")" 2>/dev/null
}

install_from_release() {
    apt_install curl ca-certificates unzip
    local url archive
    url="$(release_asset_url "$RELEASE_TAG")"
    archive="${WORK_DIR}/XrayR.zip"
    log "downloading ${url}"
    curl -fsSL --retry 3 --retry-delay 2 -o "$archive" "$url" ||
        die "failed to download the release archive for tag '${RELEASE_TAG}'"

    mkdir -p "${WORK_DIR}/archive"
    unzip -q -o "$archive" -d "${WORK_DIR}/archive"
    [ -f "${WORK_DIR}/archive/XrayR" ] || die "the release archive does not contain an XrayR binary"

    install_binary "${WORK_DIR}/archive/XrayR"
    install_support_files "${WORK_DIR}/archive"
}

install_from_source() {
    apt_install curl ca-certificates tar git
    ensure_go

    local src="$SOURCE_DIR"
    if [ -z "$src" ]; then
        src="${WORK_DIR}/src"
        log "cloning ${REPO_SLUG} at ${REF}"
        git clone --quiet --depth 1 --branch "$REF" "https://github.com/${REPO_SLUG}.git" "$src" ||
            die "failed to clone ${REPO_SLUG}; pass --source-dir to build an existing checkout"
    else
        [ -f "${src}/go.mod" ] || die "${src} does not look like an XrayR checkout (no go.mod)"
        log "building from ${src}"
    fi

    local output="${WORK_DIR}/XrayR"
    ( cd "$src" &&
      GOFLAGS="-trimpath" CGO_ENABLED=0 go build -ldflags "-s -w" -o "$output" . ) ||
        die "go build failed"

    install_binary "$output"
    install_support_files "${src}/release/config"
    LOCAL_ROOT="$src"
}

# -------------------------------------------------------------------- installation

install_binary() {
    local source_binary="$1"
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$source_binary" "${INSTALL_DIR}/XrayR"
    ok "installed ${INSTALL_DIR}/XrayR ($("${INSTALL_DIR}/XrayR" version 2>/dev/null || echo 'version check failed'))"
}

install_support_files() {
    local source_dir="$1"
    mkdir -p "$CONFIG_DIR"
    chmod 0700 "$CONFIG_DIR"

    local file
    for file in dns.json route.json custom_inbound.json custom_outbound.json rulelist; do
        if [ -f "${source_dir}/${file}" ] && [ ! -f "${CONFIG_DIR}/${file}" ]; then
            install -m 0644 "${source_dir}/${file}" "${CONFIG_DIR}/${file}"
        fi
    done
    # Release archives already ship the rule data; source builds download it below.
    for file in geoip.dat geosite.dat; do
        if [ -f "${source_dir}/${file}" ] && [ ! -f "${CONFIG_DIR}/${file}" ]; then
            install -m 0644 "${source_dir}/${file}" "${CONFIG_DIR}/${file}"
        fi
    done
}

install_rules() {
    local script="${WORK_DIR}/download-rules-dat.sh"
    log "fetching geoip.dat / geosite.dat"
    fetch_repo_file "release/download-rules-dat.sh" "$script" ||
        die "failed to obtain release/download-rules-dat.sh"
    bash "$script" "$CONFIG_DIR"
    ok "rule data installed in ${CONFIG_DIR}"
}

install_service() {
    local unit="${WORK_DIR}/XrayR.service"
    log "installing systemd unit"
    fetch_repo_file "release/systemd/XrayR.service" "$unit" ||
        die "failed to obtain release/systemd/XrayR.service"

    # Honour a non-default --config-dir / --install-dir in the generated unit.
    if [ "$CONFIG_DIR" != "/etc/XrayR" ] || [ "$INSTALL_DIR" != "/usr/local/bin" ]; then
        sed -i \
            -e "s#/etc/XrayR#${CONFIG_DIR}#g" \
            -e "s#/usr/local/bin/XrayR#${INSTALL_DIR}/XrayR#g" \
            "$unit"
    fi

    install -m 0644 "$unit" "$SERVICE_PATH"
    if [ "$HAS_SYSTEMD" -eq 1 ]; then
        systemctl daemon-reload
        ok "installed ${SERVICE_PATH}"
    else
        warn "unit written to ${SERVICE_PATH} but systemd is not running"
    fi
}

enable_service() {
    [ "$HAS_SYSTEMD" -eq 1 ] || return 0
    [ -f "${CONFIG_DIR}/config.yml" ] || return 0
    log "enabling and starting ${SERVICE_NAME}"
    systemctl enable --now "$SERVICE_NAME"
    ok "service started"
}

do_uninstall() {
    log "uninstalling XrayR"
    if [ "$HAS_SYSTEMD" -eq 1 ]; then
        systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
    fi
    rm -f "$SERVICE_PATH"
    [ "$HAS_SYSTEMD" -eq 1 ] && systemctl daemon-reload
    rm -f "${INSTALL_DIR}/XrayR"
    ok "removed the binary and the systemd unit"

    if [ "$PURGE" -eq 1 ]; then
        rm -rf "$CONFIG_DIR"
        ok "removed ${CONFIG_DIR}"
    else
        printf '  %s\n' "configuration kept in ${CONFIG_DIR} (use --purge to delete it)"
    fi
}

# -------------------------------------------------------------------------- main

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --release)
                MODE="release"
                if [ $# -gt 1 ] && [ "${2#--}" = "$2" ]; then
                    RELEASE_TAG="$2"; shift
                fi
                ;;
            --build)        MODE="source" ;;
            --source-dir)   SOURCE_DIR="${2:?--source-dir needs a path}"; shift ;;
            --ref)          REF="${2:?--ref needs a value}"; shift ;;
            --go-version)   GO_VERSION="${2:?--go-version needs a value}"; shift ;;
            --install-dir)  INSTALL_DIR="${2:?--install-dir needs a path}"; shift ;;
            --config-dir)   CONFIG_DIR="${2:?--config-dir needs a path}"; shift ;;
            --skip-rules)   SKIP_RULES=1 ;;
            --skip-service) SKIP_SERVICE=1 ;;
            --no-start)     START_SERVICE=0 ;;
            --init)         RUN_INIT=1 ;;
            --uninstall)    DO_UNINSTALL=1 ;;
            --purge)        PURGE=1 ;;
            -h|--help)      usage; exit 0 ;;
            *) die "unknown option '$1' (try --help)" ;;
        esac
        shift
    done
}

main() {
    # When this script sits inside a checkout we can use its files directly.
    local script_dir
    script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
    if [ -f "${script_dir}/download-rules-dat.sh" ] && [ -f "${script_dir}/../go.mod" ]; then
        LOCAL_ROOT="$(cd -- "${script_dir}/.." && pwd)"
    else
        LOCAL_ROOT=""
    fi

    parse_args "$@"
    require_root

    WORK_DIR="$(mktemp -d)"
    detect_platform

    if [ "$DO_UNINSTALL" -eq 1 ]; then
        do_uninstall
        return 0
    fi

    if [ "$MODE" = "auto" ]; then
        if release_exists; then
            MODE="release"
            log "found a release archive for ${ASSET}; using it"
        else
            MODE="source"
            log "no release archive available; building from source"
        fi
    fi

    if [ "$MODE" = "release" ]; then
        install_from_release
    else
        install_from_source
    fi

    if [ "$SKIP_RULES" -eq 0 ] && { [ ! -f "${CONFIG_DIR}/geoip.dat" ] || [ ! -f "${CONFIG_DIR}/geosite.dat" ]; }; then
        install_rules
    fi

    if [ "$SKIP_SERVICE" -eq 0 ]; then
        install_service
    fi

    if [ "$RUN_INIT" -eq 1 ]; then
        if [ -t 0 ]; then
            log "starting the configuration wizard"
            "${INSTALL_DIR}/XrayR" config init --output "${CONFIG_DIR}/config.yml"
        else
            warn "--init needs an interactive terminal; run it manually:"
            printf '  %s\n' "${INSTALL_DIR}/XrayR config init --output ${CONFIG_DIR}/config.yml"
        fi
    fi

    printf '\n'
    if [ -f "${CONFIG_DIR}/config.yml" ]; then
        log "validating ${CONFIG_DIR}/config.yml"
        if "${INSTALL_DIR}/XrayR" config check -c "${CONFIG_DIR}/config.yml"; then
            [ "$SKIP_SERVICE" -eq 0 ] && [ "$START_SERVICE" -eq 1 ] && enable_service
        else
            warn "configuration is invalid; fix it before starting the service"
        fi
    else
        warn "no configuration yet at ${CONFIG_DIR}/config.yml"
        printf '  %s\n' "create one with: ${INSTALL_DIR}/XrayR config init --output ${CONFIG_DIR}/config.yml"
    fi

    local rules_status="${CONFIG_DIR}/geoip.dat, ${CONFIG_DIR}/geosite.dat"
    if [ ! -f "${CONFIG_DIR}/geoip.dat" ] || [ ! -f "${CONFIG_DIR}/geosite.dat" ]; then
        rules_status="missing (run: bash release/download-rules-dat.sh ${CONFIG_DIR})"
    fi

    cat <<EOF

${C_BOLD}XrayR is installed${C_RESET}

  binary    ${INSTALL_DIR}/XrayR
  config    ${CONFIG_DIR}/config.yml
  rules     ${rules_status}
  unit      ${SERVICE_PATH}

Next steps:
  ${INSTALL_DIR}/XrayR config init --output ${CONFIG_DIR}/config.yml
  ${INSTALL_DIR}/XrayR doctor -c ${CONFIG_DIR}/config.yml
  systemctl enable --now ${SERVICE_NAME}
EOF
}

main "$@"
