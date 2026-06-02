// Package backend abstracts the two ways we manage WiFi on Debian:
//
//   - NetworkManager via nmcli  (preferred, used when NM is running)
//   - hostapd + wpa_supplicant  (fallback, used on bare wpa_supplicant systems)
//
// Detect() picks the right one at runtime so the same binary works on both.
package backend

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"wifi-provisioner/internal/logx"
)

// Network is a single scanned WiFi access point.
type Network struct {
	SSID   string `json:"ssid"`
	Signal int    `json:"signal"` // 0-100, higher is stronger
	Secure bool   `json:"secure"`
}

// Backend manages the WiFi radio for provisioning and for joining a network.
type Backend interface {
	// Name identifies the implementation (for logs / the UI).
	Name() string
	// Scan lists nearby networks. Call it in station mode, before StartAP,
	// because single-radio adapters cannot scan while beaconing as an AP.
	Scan(ctx context.Context, iface string) ([]Network, error)
	// StartAP brings the interface up as an access point with a static address.
	// cidr is e.g. "192.168.4.1/24".
	StartAP(ctx context.Context, iface, ssid, pass, cidr string) error
	// StopAP tears the access point down.
	StopAP(ctx context.Context, iface string) error
	// Connect joins the given network as a station and obtains an IP address.
	// It returns once associated + addressed, or on error/timeout (via ctx).
	Connect(ctx context.Context, iface, ssid, pass string) error
}

// Detect returns the NetworkManager backend when NM is running, else the raw
// hostapd/wpa_supplicant backend.
func Detect(log *logx.Logger) Backend {
	if nmRunning() {
		log.Infof("network backend: NetworkManager (nmcli)")
		return &nmBackend{log: log}
	}
	log.Infof("network backend: hostapd + wpa_supplicant (raw)")
	return &rawBackend{log: log}
}

func nmRunning() bool {
	out, err := exec.Command("nmcli", "-t", "-f", "RUNNING", "general").Output()
	if err == nil && strings.Contains(strings.ToLower(string(out)), "running") {
		return true
	}
	if err := exec.Command("systemctl", "is-active", "--quiet", "NetworkManager").Run(); err == nil {
		return true
	}
	return false
}

// run executes a command, capturing combined output for error context.
func run(ctx context.Context, log *logx.Logger, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	log.Debugf("exec: %s %s", name, strings.Join(args, " "))
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, out)
	}
	return out, nil
}

// runOK runs a command and ignores failure (best-effort cleanup steps).
func runOK(ctx context.Context, log *logx.Logger, name string, args ...string) {
	if _, err := run(ctx, log, name, args...); err != nil {
		log.Debugf("ignored error: %v", err)
	}
}
