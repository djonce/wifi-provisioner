#!/usr/bin/env bash
# Remove wifi-provisioner. Run as root:  sudo ./uninstall.sh
# Config and state under /etc and /var are kept unless --purge is given.
set -euo pipefail

BIN_NAME="wifi-provisioner"

if [[ "${EUID}" -ne 0 ]]; then
  echo "ERROR: please run as root (sudo ./uninstall.sh)" >&2
  exit 1
fi

systemctl stop "${BIN_NAME}.service" 2>/dev/null || true
systemctl disable "${BIN_NAME}.service" 2>/dev/null || true
rm -f "/etc/systemd/system/${BIN_NAME}.service"
systemctl daemon-reload
rm -f "/usr/local/bin/${BIN_NAME}"
echo ">> removed binary and service."

if [[ "${1:-}" == "--purge" ]]; then
  rm -rf /etc/wifi-provisioner /var/lib/wifi-provisioner
  echo ">> purged /etc/wifi-provisioner and /var/lib/wifi-provisioner."
else
  echo ">> kept config (/etc/wifi-provisioner) and state (/var/lib/wifi-provisioner). Use --purge to remove."
fi
