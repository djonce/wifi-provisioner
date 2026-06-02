#!/usr/bin/env bash
# Install wifi-provisioner: binary + config + systemd service.
# Run as root ON THE BOARD:  sudo ./install.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_NAME="wifi-provisioner"
BIN_DST="/usr/local/bin/${BIN_NAME}"
CONF_DIR="/etc/wifi-provisioner"
STATE_DIR="/var/lib/wifi-provisioner"
UNIT_DST="/etc/systemd/system/${BIN_NAME}.service"

if [[ "${EUID}" -ne 0 ]]; then
  echo "ERROR: please run as root (sudo ./install.sh)" >&2
  exit 1
fi

# --- obtain the binary -------------------------------------------------------
SRC_BIN=""
if [[ -x "${SCRIPT_DIR}/bin/${BIN_NAME}" ]]; then
  SRC_BIN="${SCRIPT_DIR}/bin/${BIN_NAME}"
elif command -v go >/dev/null 2>&1; then
  echo ">> bin/${BIN_NAME} not found; building from source with go..."
  ( cd "${SCRIPT_DIR}" && make build )
  SRC_BIN="${SCRIPT_DIR}/bin/${BIN_NAME}"
else
  echo "ERROR: no prebuilt bin/${BIN_NAME} and Go is not installed." >&2
  echo "       Build it on a machine with Go:  make build" >&2
  exit 1
fi

echo ">> installing binary -> ${BIN_DST}"
install -m 0755 "${SRC_BIN}" "${BIN_DST}"

# --- config ------------------------------------------------------------------
mkdir -p "${CONF_DIR}" "${STATE_DIR}"
if [[ ! -f "${CONF_DIR}/config.json" ]]; then
  echo ">> installing default config -> ${CONF_DIR}/config.json"
  install -m 0644 "${SCRIPT_DIR}/config.example.json" "${CONF_DIR}/config.json"
else
  echo ">> keeping existing ${CONF_DIR}/config.json"
fi

# --- systemd unit ------------------------------------------------------------
echo ">> installing service -> ${UNIT_DST}"
install -m 0644 "${SCRIPT_DIR}/${BIN_NAME}.service" "${UNIT_DST}"
systemctl daemon-reload
systemctl enable "${BIN_NAME}.service"

# --- dependency check --------------------------------------------------------
echo ">> checking runtime dependencies..."
have() { command -v "$1" >/dev/null 2>&1; }
if have nmcli && systemctl is-active --quiet NetworkManager; then
  echo "   NetworkManager detected: good (nmcli path will be used)."
else
  echo "   NetworkManager NOT active -> raw path. These tools are required:"
  for t in hostapd wpa_supplicant iw; do
    if have "$t"; then echo "     [ok]  $t"; else echo "     [MISSING] $t  (apt install $t)"; fi
  done
  if have udhcpc || have dhclient; then echo "     [ok]  dhcp client"; else
    echo "     [MISSING] dhcp client (apt install udhcpc OR isc-dhcp-client)"; fi
fi
if ! have gpioget; then
  echo "   note: 'gpioget' not found -> GPIO button trigger disabled (apt install gpiod). Sentinel file still works."
fi

cat <<EOF

Done. Next steps:
  - Review config:   ${CONF_DIR}/config.json
  - Start now:       systemctl start ${BIN_NAME}
  - Watch logs:      journalctl -u ${BIN_NAME} -f
  - Re-provision:    touch ${STATE_DIR}/reconfigure   (then it reopens the hotspot)

The service is enabled and will run automatically on every boot.
EOF
