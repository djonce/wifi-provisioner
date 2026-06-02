package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wifi-provisioner/internal/logx"
)

type rawBackend struct {
	log     *logx.Logger
	mu      sync.Mutex
	hostapd *exec.Cmd
}

func (b *rawBackend) Name() string { return "hostapd/wpa_supplicant" }

func (b *rawBackend) Scan(ctx context.Context, iface string) ([]Network, error) {
	runOK(ctx, b.log, "ip", "link", "set", iface, "up")
	out, err := run(ctx, b.log, "iw", "dev", iface, "scan")
	if err != nil {
		return nil, err
	}
	return parseIWScan(out), nil
}

func (b *rawBackend) StartAP(ctx context.Context, iface, ssid, pass, cidr string) error {
	b.stopWPA(ctx, iface)

	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return err
	}
	runOK(ctx, b.log, "ip", "link", "set", iface, "down")
	runOK(ctx, b.log, "ip", "addr", "flush", "dev", iface)
	if _, err := run(ctx, b.log, "ip", "link", "set", iface, "up"); err != nil {
		return err
	}
	if _, err := run(ctx, b.log, "ip", "addr", "add", fmt.Sprintf("%s/%d", ip, prefix), "dev", iface); err != nil {
		return err
	}

	confPath, err := b.writeHostapdConf(iface, ssid, pass)
	if err != nil {
		return err
	}

	cmd := exec.Command("hostapd", confPath)
	cmd.Stdout = newLogWriter(b.log, "hostapd")
	cmd.Stderr = newLogWriter(b.log, "hostapd")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hostapd: %w", err)
	}
	b.mu.Lock()
	b.hostapd = cmd
	b.mu.Unlock()

	// Give hostapd a moment; if it died immediately, surface the error.
	time.Sleep(1500 * time.Millisecond)
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return fmt.Errorf("hostapd exited early (driver may not support AP mode)")
	}
	return nil
}

func (b *rawBackend) StopAP(ctx context.Context, iface string) error {
	b.mu.Lock()
	cmd := b.hostapd
	b.hostapd = nil
	b.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	runOK(ctx, b.log, "ip", "addr", "flush", "dev", iface)
	runOK(ctx, b.log, "ip", "link", "set", iface, "down")
	return nil
}

func (b *rawBackend) Connect(ctx context.Context, iface, ssid, pass string) error {
	b.stopWPA(ctx, iface)
	runOK(ctx, b.log, "ip", "addr", "flush", "dev", iface)
	if _, err := run(ctx, b.log, "ip", "link", "set", iface, "up"); err != nil {
		return err
	}

	confPath, err := b.writeWPAConf(iface, ssid, pass)
	if err != nil {
		return err
	}
	if _, err := run(ctx, b.log, "wpa_supplicant", "-B", "-i", iface, "-c", confPath); err != nil {
		return err
	}

	// Wait for association.
	if err := b.waitAssociated(ctx, iface); err != nil {
		return err
	}

	// Obtain an address (udhcpc preferred, dhclient fallback).
	if _, err := exec.LookPath("udhcpc"); err == nil {
		_, err = run(ctx, b.log, "udhcpc", "-i", iface, "-q", "-n", "-t", "8")
		return err
	}
	if _, err := exec.LookPath("dhclient"); err == nil {
		_, err = run(ctx, b.log, "dhclient", "-1", iface)
		return err
	}
	return fmt.Errorf("no DHCP client found (install udhcpc or isc-dhcp-client)")
}

func (b *rawBackend) waitAssociated(ctx context.Context, iface string) error {
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		out, _ := run(ctx, b.log, "iw", "dev", iface, "link")
		if strings.Contains(out, "Connected to") {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for association to network")
}

func (b *rawBackend) stopWPA(ctx context.Context, iface string) {
	runOK(ctx, b.log, "pkill", "-f", "wpa_supplicant.*"+iface)
	time.Sleep(300 * time.Millisecond)
}

func (b *rawBackend) writeHostapdConf(iface, ssid, pass string) (string, error) {
	dir := "/run/wifi-provisioner"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "interface=%s\n", iface)
	sb.WriteString("driver=nl80211\n")
	fmt.Fprintf(&sb, "ssid=%s\n", ssid)
	sb.WriteString("hw_mode=g\nchannel=6\nauth_algs=1\nwmm_enabled=0\n")
	if pass != "" {
		sb.WriteString("wpa=2\n")
		fmt.Fprintf(&sb, "wpa_passphrase=%s\n", pass)
		sb.WriteString("wpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\n")
	}
	path := filepath.Join(dir, "hostapd.conf")
	return path, os.WriteFile(path, []byte(sb.String()), 0o600)
}

func (b *rawBackend) writeWPAConf(iface, ssid, pass string) (string, error) {
	dir := "/etc/wpa_supplicant"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("ctrl_interface=/run/wpa_supplicant\nupdate_config=1\n\n")
	sb.WriteString("network={\n")
	fmt.Fprintf(&sb, "\tssid=%q\n", ssid)
	if pass == "" {
		sb.WriteString("\tkey_mgmt=NONE\n")
	} else {
		fmt.Fprintf(&sb, "\tpsk=%q\n", pass)
	}
	sb.WriteString("}\n")
	path := filepath.Join(dir, "wpa_supplicant-"+iface+".conf")
	return path, os.WriteFile(path, []byte(sb.String()), 0o600)
}

// parseIWScan extracts SSID, signal and security from `iw dev <iface> scan`.
func parseIWScan(out string) []Network {
	best := map[string]Network{}
	var cur Network
	var have bool
	flush := func() {
		if have && cur.SSID != "" {
			if prev, ok := best[cur.SSID]; ok {
				if cur.Signal > prev.Signal {
					prev.Signal = cur.Signal
				}
				prev.Secure = prev.Secure || cur.Secure
				best[cur.SSID] = prev
			} else {
				best[cur.SSID] = cur
			}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "BSS "):
			flush()
			cur = Network{}
			have = true
		case strings.HasPrefix(t, "signal:"):
			cur.Signal = dbmToQuality(t)
		case strings.HasPrefix(t, "SSID:"):
			cur.SSID = strings.TrimSpace(strings.TrimPrefix(t, "SSID:"))
		case strings.HasPrefix(t, "RSN:") || strings.HasPrefix(t, "WPA:"):
			cur.Secure = true
		}
	}
	flush()
	return sortedNetworks(best)
}

// dbmToQuality maps "signal: -57.00 dBm" to a rough 0-100 quality value.
func dbmToQuality(line string) int {
	fields := strings.Fields(line) // ["signal:", "-57.00", "dBm"]
	if len(fields) < 2 {
		return 0
	}
	dbm, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0
	}
	q := int(2 * (dbm + 100)) // -100dBm=>0, -50dBm=>100
	if q < 0 {
		q = 0
	}
	if q > 100 {
		q = 100
	}
	return q
}

func splitCIDR(cidr string) (ip string, prefix int, err error) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid cidr %q", cidr)
	}
	prefix, err = strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid cidr prefix %q", cidr)
	}
	return parts[0], prefix, nil
}

// logWriter forwards child-process output to our logger line by line.
type logWriter struct {
	log *logx.Logger
	tag string
}

func newLogWriter(log *logx.Logger, tag string) *logWriter { return &logWriter{log: log, tag: tag} }

func (w *logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.log.Debugf("[%s] %s", w.tag, line)
		}
	}
	return len(p), nil
}
