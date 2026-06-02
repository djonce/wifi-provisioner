// Command wifi-provisioner brings up a WiFi setup hotspot + captive portal on a
// headless device whenever it has no internet connection, lets a phone configure
// the target network, then joins it and tears the hotspot down.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"wifi-provisioner/internal/app"
	"wifi-provisioner/internal/backend"
	"wifi-provisioner/internal/config"
	"wifi-provisioner/internal/logx"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/wifi-provisioner/config.json", "path to JSON config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("wifi-provisioner", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	if *debug {
		cfg.Debug = true
	}

	log := logx.New(cfg.Debug)
	log.Infof("wifi-provisioner %s starting (iface=%s, ap=%q)", version, cfg.Iface, cfg.APSSID)

	if os.Geteuid() != 0 {
		log.Warnf("not running as root: managing WiFi and binding ports 53/67/80 will likely fail")
	}

	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		log.Warnf("could not create state dir %s: %v", cfg.StateDir, err)
	}

	be := backend.Detect(log)
	application := app.New(cfg, be, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Infof("shutdown signal received; cleaning up")
	}()

	application.Run(ctx)
	application.Shutdown()
	log.Infof("wifi-provisioner stopped")
}
