// Package config loads and normalizes runtime configuration.
//
// Configuration comes from a JSON file (optional). Any field not present in the
// file keeps its built-in default. Empty "auto" fields (interface, AP SSID) are
// resolved at startup from the hardware.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

type Config struct {
	// WiFi interface to use. Empty => auto-detect the first wireless interface.
	Iface string `json:"iface"`

	// Access point (provisioning hotspot) settings.
	APSSID     string `json:"ap_ssid"`     // empty => "CubieSetup-XXXX" from MAC
	APPassword string `json:"ap_password"` // empty => open network
	APAddress  string `json:"ap_address"`  // gateway/portal IP on the hotspot
	APPrefix   int    `json:"ap_prefix"`   // netmask prefix length

	// DHCP pool handed out to clients that join the hotspot.
	DHCPStart    string `json:"dhcp_start"`
	DHCPEnd      string `json:"dhcp_end"`
	LeaseMinutes int    `json:"lease_minutes"`

	// Captive portal web server port.
	WebPort int `json:"web_port"`

	// Timeouts and intervals.
	ProvisionTimeoutMin int `json:"provision_timeout_min"` // 0 => no timeout
	ConnectTimeoutSec   int `json:"connect_timeout_sec"`
	OnlineTimeoutSec    int `json:"online_timeout_sec"`
	CheckIntervalSec    int `json:"check_interval_sec"`

	// URLs used to decide whether the device already has internet access.
	// A 204 response means online.
	ConnectivityURLs []string `json:"connectivity_urls"`

	// State / trigger paths.
	StateDir     string `json:"state_dir"`
	SentinelFile string `json:"sentinel_file"`

	// Optional GPIO push-button to re-enter provisioning. Requires the
	// libgpiod "gpioget" tool to be installed. Chip empty => disabled.
	GPIOChip      string `json:"gpio_chip"`       // e.g. "gpiochip0"
	GPIOLine      int    `json:"gpio_line"`       // line offset on the chip
	GPIOActiveLow bool   `json:"gpio_active_low"` // true if button pulls line low
	GPIOHoldSec   int    `json:"gpio_hold_sec"`   // how long it must be held

	Debug bool `json:"debug"`
}

func Default() *Config {
	return &Config{
		Iface:               "",
		APSSID:              "",
		APPassword:          "",
		APAddress:           "192.168.4.1",
		APPrefix:            24,
		DHCPStart:           "192.168.4.50",
		DHCPEnd:             "192.168.4.150",
		LeaseMinutes:        10,
		WebPort:             80,
		ProvisionTimeoutMin: 0,
		ConnectTimeoutSec:   45,
		OnlineTimeoutSec:    5,
		CheckIntervalSec:    30,
		ConnectivityURLs: []string{
			"http://connectivitycheck.gstatic.com/generate_204",
			"http://connect.rom.miui.com/generate_204",
			"http://wifi.vivo.com.cn/generate_204",
		},
		StateDir:      "/var/lib/wifi-provisioner",
		SentinelFile:  "/var/lib/wifi-provisioner/reconfigure",
		GPIOChip:      "",
		GPIOLine:      0,
		GPIOActiveLow: true,
		GPIOHoldSec:   3,
		Debug:         false,
	}
}

// Load reads the config file (if any) on top of the defaults.
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// fall through: use defaults
		default:
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) normalize() error {
	if c.Iface == "" {
		iface, err := detectWirelessIface()
		if err != nil {
			return fmt.Errorf("auto-detect wireless interface: %w", err)
		}
		c.Iface = iface
	}
	if c.APSSID == "" {
		c.APSSID = "CubieSetup-" + macSuffix(c.Iface)
	}
	if c.APPrefix <= 0 || c.APPrefix > 30 {
		c.APPrefix = 24
	}
	if net.ParseIP(c.APAddress) == nil {
		return fmt.Errorf("invalid ap_address %q", c.APAddress)
	}
	return nil
}

// CIDR returns the hotspot address in CIDR form, e.g. "192.168.4.1/24".
func (c *Config) CIDR() string {
	return fmt.Sprintf("%s/%d", c.APAddress, c.APPrefix)
}

// Netmask returns the IPv4 mask matching APPrefix.
func (c *Config) Netmask() net.IPMask {
	return net.CIDRMask(c.APPrefix, 32)
}

// detectWirelessIface returns the first interface that has a wireless sysfs node.
func detectWirelessIface() (string, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		name := e.Name()
		// A wireless device exposes either a "wireless" dir or a "phy80211" link.
		if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
			return name, nil
		}
		if _, err := os.Stat("/sys/class/net/" + name + "/phy80211"); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no wireless interface found under /sys/class/net")
}

// macSuffix returns the upper-case last two octets of the interface MAC, e.g. "A1B2".
func macSuffix(iface string) string {
	b, err := os.ReadFile("/sys/class/net/" + iface + "/address")
	if err != nil {
		return "0000"
	}
	mac := strings.TrimSpace(string(b))
	parts := strings.Split(mac, ":")
	if len(parts) < 2 {
		return "0000"
	}
	return strings.ToUpper(parts[len(parts)-2] + parts[len(parts)-1])
}
