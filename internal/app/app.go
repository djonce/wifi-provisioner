// Package app is the provisioning state machine that wires together the WiFi
// backend, the captive portal services and the re-provisioning triggers.
//
// Lifecycle:
//
//	online?  --no--> provision() : scan -> AP up -> portal -> wait for join
//	   |yes
//	   v
//	idle()  : stay up; re-provision on trigger or when the link drops
package app

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"wifi-provisioner/internal/backend"
	"wifi-provisioner/internal/config"
	"wifi-provisioner/internal/detect"
	"wifi-provisioner/internal/logx"
	"wifi-provisioner/internal/portal"
	"wifi-provisioner/internal/trigger"
)

type App struct {
	cfg *config.Config
	be  backend.Backend
	log *logx.Logger

	mu       sync.Mutex
	status   portal.Status
	networks []backend.Network
	lastSSID string

	dhcp *portal.DHCPServer
	dns  *portal.DNSServer
	web  *portal.Server
	apUp bool

	sess     *session
	connBusy bool

	reprovision chan struct{}
}

type session struct {
	connected chan struct{}
	once      sync.Once
}

func New(cfg *config.Config, be backend.Backend, log *logx.Logger) *App {
	return &App{
		cfg:         cfg,
		be:          be,
		log:         log,
		reprovision: make(chan struct{}, 1),
	}
}

type reason int

const (
	reasonStop reason = iota
	reasonTrigger
	reasonOffline
)

// Run drives the state machine until ctx is cancelled.
func (a *App) Run(ctx context.Context) {
	go trigger.WatchSentinel(ctx, a.cfg.SentinelFile, 2*time.Second, a.fireReprovision, a.log)
	go trigger.WatchGPIO(ctx, a.cfg.GPIOChip, a.cfg.GPIOLine, a.cfg.GPIOActiveLow,
		time.Duration(a.cfg.GPIOHoldSec)*time.Second, a.fireReprovision, a.log)

	for {
		if ctx.Err() != nil {
			return
		}
		if !a.online() {
			a.provision(ctx)
			continue
		}
		switch a.idle(ctx) {
		case reasonStop:
			return
		case reasonTrigger:
			a.log.Infof("re-provision requested")
			a.provision(ctx)
		case reasonOffline:
			a.log.Warnf("internet connectivity lost")
			// loop: online() will be false and we re-provision
		}
	}
}

// Shutdown tears down any active hotspot (called on exit).
func (a *App) Shutdown() {
	a.stopPortal(context.Background())
}

func (a *App) idle(ctx context.Context) reason {
	a.mu.Lock()
	ssid := a.lastSSID
	a.mu.Unlock()
	a.setStatus(portal.Status{State: "connected", SSID: ssid})
	a.log.Infof("device is online; idle (waiting for trigger or link loss)")

	t := time.NewTicker(time.Duration(a.cfg.CheckIntervalSec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return reasonStop
		case <-a.reprovision:
			return reasonTrigger
		case <-t.C:
			if !a.online() {
				return reasonOffline
			}
		}
	}
}

func (a *App) provision(ctx context.Context) {
	a.log.Infof("entering provisioning mode")

	sess := &session{connected: make(chan struct{})}
	a.mu.Lock()
	a.sess = sess
	a.mu.Unlock()
	a.setStatus(portal.Status{State: "provisioning"})

	a.doScan(ctx) // best-effort: must happen before AP comes up (single radio)

	if err := a.startPortal(ctx); err != nil {
		a.log.Errorf("failed to start provisioning portal: %v", err)
		sleepCtx(ctx, 10*time.Second)
		return
	}

	var timeout <-chan time.Time
	if a.cfg.ProvisionTimeoutMin > 0 {
		tm := time.NewTimer(time.Duration(a.cfg.ProvisionTimeoutMin) * time.Minute)
		defer tm.Stop()
		timeout = tm.C
	}

	select {
	case <-ctx.Done():
	case <-sess.connected:
		a.log.Infof("provisioning succeeded")
	case <-timeout:
		a.log.Warnf("provisioning timed out with no successful connection")
	}
	a.stopPortal(ctx)
}

// handleConnect is invoked by the web portal when the user submits credentials.
// It runs asynchronously so the HTTP request returns immediately.
func (a *App) handleConnect(ssid, pass string) {
	a.mu.Lock()
	if a.connBusy {
		a.mu.Unlock()
		a.log.Infof("ignoring connect request: attempt already in progress")
		return
	}
	a.connBusy = true
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.connBusy = false
			a.mu.Unlock()
		}()

		a.log.Infof("attempting to join %q", ssid)
		a.setStatus(portal.Status{State: "connecting", SSID: ssid})

		// Let the HTTP response flush before the hotspot disappears.
		time.Sleep(1500 * time.Millisecond)

		// Single-radio adapters cannot be AP and station at once: drop the portal.
		a.stopPortal(context.Background())

		cctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(a.cfg.ConnectTimeoutSec)*time.Second)
		err := a.be.Connect(cctx, a.cfg.Iface, ssid, pass)
		cancel()

		if err == nil && a.online() {
			a.mu.Lock()
			a.lastSSID = ssid
			a.mu.Unlock()
			a.setStatus(portal.Status{State: "connected", SSID: ssid})
			a.log.Infof("successfully connected to %q", ssid)
			a.signalConnected()
			return
		}

		msg := "请检查密码后重试"
		if err != nil {
			msg = err.Error()
		}
		a.log.Warnf("failed to join %q: %v", ssid, err)
		a.setStatus(portal.Status{State: "failed", SSID: ssid, Message: msg})

		// Bring the hotspot back so the user can retry from their phone.
		if err := a.startPortal(context.Background()); err != nil {
			a.log.Errorf("could not restart portal after failure: %v", err)
		}
	}()
}

func (a *App) signalConnected() {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess != nil {
		sess.once.Do(func() { close(sess.connected) })
	}
}

func (a *App) startPortal(ctx context.Context) error {
	a.mu.Lock()
	if a.apUp {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	if err := a.be.StartAP(ctx, a.cfg.Iface, a.cfg.APSSID, a.cfg.APPassword, a.cfg.CIDR()); err != nil {
		return fmt.Errorf("start access point: %w", err)
	}

	apIP := net.ParseIP(a.cfg.APAddress)
	if err := waitForIP(a.cfg.Iface, apIP, 10*time.Second); err != nil {
		a.log.Warnf("hotspot IP %s not confirmed on %s: %v", a.cfg.APAddress, a.cfg.Iface, err)
	}

	dhcp := portal.NewDHCPServer(a.cfg.Iface, apIP, a.cfg.Netmask(),
		net.ParseIP(a.cfg.DHCPStart), net.ParseIP(a.cfg.DHCPEnd),
		time.Duration(a.cfg.LeaseMinutes)*time.Minute, a.log)
	if err := dhcp.Start(); err != nil {
		a.be.StopAP(ctx, a.cfg.Iface)
		return fmt.Errorf("start dhcp: %w", err)
	}

	// DNS hijack is best-effort (port 53 may be taken by resolved); the portal
	// is still reachable by IP without it.
	dns := portal.NewDNSServer(a.cfg.Iface, apIP, apIP, a.log)
	if err := dns.Start(); err != nil {
		a.log.Warnf("DNS hijack disabled (captive auto-popup may not work): %v", err)
		dns = nil
	}

	web := portal.NewServer(apIP, a.cfg.WebPort, a.handlers(), a.log)
	if err := web.Start(); err != nil {
		dhcp.Stop()
		if dns != nil {
			dns.Stop()
		}
		a.be.StopAP(ctx, a.cfg.Iface)
		return fmt.Errorf("start web: %w", err)
	}

	a.mu.Lock()
	a.dhcp, a.dns, a.web, a.apUp = dhcp, dns, web, true
	a.mu.Unlock()
	a.log.Infof("hotspot %q is up — connect to it, then open http://%s", a.cfg.APSSID, a.cfg.APAddress)
	return nil
}

func (a *App) stopPortal(ctx context.Context) {
	a.mu.Lock()
	dhcp, dns, web, up := a.dhcp, a.dns, a.web, a.apUp
	a.dhcp, a.dns, a.web, a.apUp = nil, nil, nil, false
	a.mu.Unlock()
	if !up {
		return
	}
	if web != nil {
		web.Stop()
	}
	if dns != nil {
		dns.Stop()
	}
	if dhcp != nil {
		dhcp.Stop()
	}
	a.be.StopAP(ctx, a.cfg.Iface)
	a.log.Infof("hotspot stopped")
}

func (a *App) handlers() portal.Handlers {
	return portal.Handlers{
		Networks: a.snapshotNetworks,
		Connect:  a.handleConnect,
		Status:   a.snapshotStatus,
	}
}

func (a *App) doScan(ctx context.Context) {
	nets, err := a.be.Scan(ctx, a.cfg.Iface)
	if err != nil {
		a.log.Warnf("wifi scan failed (keeping previous list): %v", err)
		return
	}
	a.mu.Lock()
	a.networks = nets
	a.mu.Unlock()
	a.log.Infof("scan found %d networks", len(nets))
}

func (a *App) snapshotNetworks() []backend.Network {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]backend.Network, len(a.networks))
	copy(out, a.networks)
	return out
}

func (a *App) snapshotStatus() portal.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *App) setStatus(s portal.Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

func (a *App) online() bool {
	return detect.IsOnline(a.cfg.ConnectivityURLs, time.Duration(a.cfg.OnlineTimeoutSec)*time.Second)
}

func (a *App) fireReprovision() {
	select {
	case a.reprovision <- struct{}{}:
	default:
	}
}

// waitForIP blocks until ip is configured on iface or the timeout elapses.
func waitForIP(iface string, ip net.IP, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ifi, err := net.InterfaceByName(iface)
		if err == nil {
			addrs, _ := ifi.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
					return nil
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s on %s", ip, iface)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
