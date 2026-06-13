#!/usr/bin/env bash
# Deploy the static website in docs/ to a Caddy-backed server.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

DOMAIN="${DOMAIN:-wifi-provisioner.19ba.cn}"
SSH_TARGET="${SSH_TARGET:-aliyun}"
SITE_DIR="${SITE_DIR:-docs}"
REMOTE_ROOT="${REMOTE_ROOT:-}"
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
SKIP_CADDY=0
SKIP_VERIFY=0

usage() {
  cat <<EOF
Usage:
  scripts/deploy-website.sh [options]

Options:
  --domain DOMAIN       Domain to serve. Default: ${DOMAIN}
  --ssh TARGET          SSH target/alias. Default: ${SSH_TARGET}
  --site-dir DIR        Local static site directory. Default: docs
  --remote-root DIR     Remote site root. Default: /var/www/<domain>
  --caddyfile FILE      Remote Caddyfile. Default: /etc/caddy/Caddyfile
  --release NAME        Release name. Default: <git-sha>[-dirty]-<timestamp>
  --skip-caddy          Upload files and switch current only; do not edit Caddy.
  --skip-verify         Do not run HTTP/HTTPS verification.
  -h, --help            Show this help.

Environment variables with the same names are also supported:
  DOMAIN, SSH_TARGET, SITE_DIR, REMOTE_ROOT, CADDYFILE, RELEASE

Examples:
  scripts/deploy-website.sh
  DOMAIN=example.com SSH_TARGET=root@example.com scripts/deploy-website.sh
  scripts/deploy-website.sh --domain example.com --ssh root@example.com
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)
      DOMAIN="${2:?missing value for --domain}"
      shift 2
      ;;
    --ssh)
      SSH_TARGET="${2:?missing value for --ssh}"
      shift 2
      ;;
    --site-dir)
      SITE_DIR="${2:?missing value for --site-dir}"
      shift 2
      ;;
    --remote-root)
      REMOTE_ROOT="${2:?missing value for --remote-root}"
      shift 2
      ;;
    --caddyfile)
      CADDYFILE="${2:?missing value for --caddyfile}"
      shift 2
      ;;
    --release)
      RELEASE="${2:?missing value for --release}"
      shift 2
      ;;
    --skip-caddy)
      SKIP_CADDY=1
      shift
      ;;
    --skip-verify)
      SKIP_VERIFY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ "${SITE_DIR}" != /* ]]; then
  SITE_DIR="${REPO_DIR}/${SITE_DIR}"
fi
REMOTE_ROOT="${REMOTE_ROOT:-/var/www/${DOMAIN}}"

if [[ ! -f "${SITE_DIR}/index.html" ]]; then
  echo "ERROR: ${SITE_DIR}/index.html not found." >&2
  exit 1
fi

need_local() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: local command not found: $1" >&2
    exit 1
  fi
}

quote() {
  printf "%q" "$1"
}

need_local ssh
need_local scp
need_local tar

if command -v node >/dev/null 2>&1 && [[ -f "${SITE_DIR}/app.js" ]]; then
  echo ">> Checking JavaScript syntax"
  node --check "${SITE_DIR}/app.js"
fi

git_sha=""
dirty_suffix=""
if git -C "${REPO_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git_sha="$(git -C "${REPO_DIR}" rev-parse --short HEAD 2>/dev/null || true)"
  if ! git -C "${REPO_DIR}" diff --quiet -- "${SITE_DIR#${REPO_DIR}/}" ||
     ! git -C "${REPO_DIR}" diff --cached --quiet -- "${SITE_DIR#${REPO_DIR}/}"; then
    dirty_suffix="-dirty"
  fi
fi

timestamp="$(date +%Y%m%d%H%M%S)"
RELEASE="${RELEASE:-${git_sha:-manual}${dirty_suffix}-${timestamp}}"
RELEASE="${RELEASE//\//-}"
ARCHIVE="/tmp/${DOMAIN}-${RELEASE}.tar.gz"
REMOTE_ARCHIVE="/tmp/${DOMAIN}-${RELEASE}.tar.gz"
REMOTE_RELEASE="${REMOTE_ROOT}/releases/${RELEASE}"

echo ">> Deployment plan"
echo "   domain:       ${DOMAIN}"
echo "   ssh target:   ${SSH_TARGET}"
echo "   site dir:     ${SITE_DIR}"
echo "   remote root:  ${REMOTE_ROOT}"
echo "   release:      ${RELEASE}"

tar_args=(-czf "${ARCHIVE}" --exclude ".DS_Store" --exclude "._*" -C "${SITE_DIR}" .)
if tar --help 2>&1 | grep -q -- "--format"; then
  tar_args=(--format ustar "${tar_args[@]}")
fi
if tar --help 2>&1 | grep -q -- "--no-xattrs"; then
  tar_args=(--no-xattrs "${tar_args[@]}")
fi

echo ">> Packing site"
COPYFILE_DISABLE=1 tar "${tar_args[@]}"

echo ">> Uploading archive"
scp "${ARCHIVE}" "${SSH_TARGET}:${REMOTE_ARCHIVE}"

echo ">> Publishing release on remote"
ssh "${SSH_TARGET}" \
  "REMOTE_ARCHIVE=$(quote "${REMOTE_ARCHIVE}") REMOTE_RELEASE=$(quote "${REMOTE_RELEASE}") REMOTE_ROOT=$(quote "${REMOTE_ROOT}") bash -se" <<'REMOTE'
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
  echo "ERROR: remote user must be root for /var/www and Caddy deployment." >&2
  exit 1
fi

install -d -m 0755 "${REMOTE_RELEASE}"
tar -xzf "${REMOTE_ARCHIVE}" -C "${REMOTE_RELEASE}"
chown -R root:root "${REMOTE_RELEASE}"
ln -sfn "${REMOTE_RELEASE}" "${REMOTE_ROOT}/current"

bad_files="$(find -L "${REMOTE_ROOT}/current" \( -name '._*' -o -name '.DS_Store' \) -print)"
if [[ -n "${bad_files}" ]]; then
  echo "ERROR: unwanted macOS metadata files were published:" >&2
  echo "${bad_files}" >&2
  exit 1
fi

echo "   current -> $(readlink -f "${REMOTE_ROOT}/current")"
REMOTE

if [[ "${SKIP_CADDY}" -eq 0 ]]; then
  echo ">> Updating Caddy site block"
  ssh "${SSH_TARGET}" \
    "DOMAIN=$(quote "${DOMAIN}") REMOTE_ROOT=$(quote "${REMOTE_ROOT}") CADDYFILE=$(quote "${CADDYFILE}") bash -se" <<'REMOTE'
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then
  echo "ERROR: remote user must be root for Caddy deployment." >&2
  exit 1
fi
if ! command -v caddy >/dev/null 2>&1; then
  echo "ERROR: caddy is not installed on the remote server." >&2
  exit 1
fi

begin="# BEGIN ${DOMAIN} managed by scripts/deploy-website.sh"
end="# END ${DOMAIN} managed by scripts/deploy-website.sh"
tmp="$(mktemp)"
tmp_new="$(mktemp)"

install -d -m 0755 "$(dirname "${CADDYFILE}")"
if [[ -f "${CADDYFILE}" ]]; then
  cp "${CADDYFILE}" "${CADDYFILE}.bak.$(date +%Y%m%d%H%M%S)"
  awk -v domain="${DOMAIN}" -v begin="${begin}" -v end="${end}" '
    function count_char(s, c, i, n) {
      n = 0
      for (i = 1; i <= length(s); i++) {
        if (substr(s, i, 1) == c) n++
      }
      return n
    }
    function delta(s) {
      return count_char(s, "{") - count_char(s, "}")
    }
    {
      line = $0
      trimmed = line
      sub(/^[ \t]+/, "", trimmed)

      if (in_marker) {
        if (line == end) in_marker = 0
        next
      }
      if (line == begin) {
        in_marker = 1
        next
      }
      if (in_domain) {
        depth += delta(line)
        if (depth <= 0) in_domain = 0
        next
      }
      if (trimmed == domain " {") {
        in_domain = 1
        depth = delta(line)
        if (depth <= 0) in_domain = 0
        next
      }

      print line
    }
  ' "${CADDYFILE}" > "${tmp}"
else
  : > "${tmp}"
fi

{
  cat "${tmp}"
  printf "\n%s\n" "${begin}"
  cat <<EOF
${DOMAIN} {
	root * ${REMOTE_ROOT}/current
	encode gzip
	header {
		X-Content-Type-Options nosniff
		Referrer-Policy strict-origin-when-cross-origin
	}
	file_server
}
EOF
  printf "%s\n" "${end}"
} > "${tmp_new}"

install -m 0644 "${tmp_new}" "${CADDYFILE}"
caddy fmt --overwrite "${CADDYFILE}" >/dev/null
caddy validate --config "${CADDYFILE}"

if systemctl is-active --quiet caddy; then
  systemctl reload caddy
else
  systemctl enable --now caddy
fi
REMOTE
fi

if [[ "${SKIP_VERIFY}" -eq 0 ]]; then
  echo ">> Verifying from remote server"
  ssh "${SSH_TARGET}" "DOMAIN=$(quote "${DOMAIN}") bash -se" <<'REMOTE'
set -euo pipefail

echo "   HTTP:"
curl -sSI --max-time 20 "http://${DOMAIN}" | sed -n '1,8p'

echo "   HTTPS:"
for attempt in $(seq 1 12); do
  if curl -sSI --max-time 20 "https://${DOMAIN}" | sed -n '1,10p'; then
    exit 0
  fi
  sleep 5
done

echo "ERROR: HTTPS verification failed after waiting." >&2
exit 1
REMOTE
fi

echo ">> Done: https://${DOMAIN}"
