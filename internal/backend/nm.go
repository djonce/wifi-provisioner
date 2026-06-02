package backend

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"wifi-provisioner/internal/logx"
)

const nmConnName = "wifiprov-hotspot"

type nmBackend struct {
	log *logx.Logger
}

func (b *nmBackend) Name() string { return "NetworkManager" }

func (b *nmBackend) Scan(ctx context.Context, iface string) ([]Network, error) {
	out, err := run(ctx, b.log, "nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY",
		"dev", "wifi", "list", "ifname", iface, "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	best := map[string]Network{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitTerse(line)
		if len(fields) < 3 {
			continue
		}
		ssid := fields[0]
		if ssid == "" {
			continue // hidden network
		}
		signal, _ := strconv.Atoi(fields[1])
		sec := strings.TrimSpace(fields[2])
		n := Network{SSID: ssid, Signal: signal, Secure: sec != "" && sec != "--"}
		if cur, ok := best[ssid]; ok {
			if n.Signal > cur.Signal {
				cur.Signal = n.Signal
			}
			cur.Secure = cur.Secure || n.Secure
			best[ssid] = cur
		} else {
			best[ssid] = n
		}
	}
	return sortedNetworks(best), nil
}

func (b *nmBackend) StartAP(ctx context.Context, iface, ssid, pass, cidr string) error {
	// Remove any stale hotspot profile first.
	runOK(ctx, b.log, "nmcli", "connection", "delete", nmConnName)

	if _, err := run(ctx, b.log, "nmcli", "connection", "add",
		"type", "wifi", "ifname", iface, "con-name", nmConnName,
		"autoconnect", "no", "ssid", ssid); err != nil {
		return err
	}
	if _, err := run(ctx, b.log, "nmcli", "connection", "modify", nmConnName,
		"802-11-wireless.mode", "ap",
		"802-11-wireless.band", "bg",
		"ipv4.method", "manual",
		"ipv4.addresses", cidr,
		"ipv6.method", "ignore"); err != nil {
		return err
	}
	if pass != "" {
		if _, err := run(ctx, b.log, "nmcli", "connection", "modify", nmConnName,
			"wifi-sec.key-mgmt", "wpa-psk",
			"wifi-sec.psk", pass); err != nil {
			return err
		}
	}
	if _, err := run(ctx, b.log, "nmcli", "connection", "up", nmConnName); err != nil {
		return err
	}
	return nil
}

func (b *nmBackend) StopAP(ctx context.Context, iface string) error {
	runOK(ctx, b.log, "nmcli", "connection", "down", nmConnName)
	runOK(ctx, b.log, "nmcli", "connection", "delete", nmConnName)
	return nil
}

func (b *nmBackend) Connect(ctx context.Context, iface, ssid, pass string) error {
	runOK(ctx, b.log, "nmcli", "device", "wifi", "rescan", "ifname", iface)
	args := []string{"device", "wifi", "connect", ssid, "ifname", iface}
	if pass != "" {
		args = []string{"device", "wifi", "connect", ssid, "password", pass, "ifname", iface}
	}
	_, err := run(ctx, b.log, "nmcli", args...)
	return err
}

// splitTerse splits an nmcli --terse line on unescaped ':' and unescapes values.
func splitTerse(line string) []string {
	var fields []string
	var cur strings.Builder
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

func sortedNetworks(m map[string]Network) []Network {
	out := make([]Network, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out
}
