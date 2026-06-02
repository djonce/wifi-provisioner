// Package trigger watches for "please re-enter provisioning" signals while the
// device is online: a sentinel file appearing, or an optional GPIO push-button.
package trigger

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"wifi-provisioner/internal/logx"
)

// WatchSentinel polls for path; when it appears it is removed and fire() runs.
// `touch /var/lib/wifi-provisioner/reconfigure` is the simplest way to trigger.
func WatchSentinel(ctx context.Context, path string, interval time.Duration, fire func(), log *logx.Logger) {
	log.Infof("watching sentinel file %s", path)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := os.Stat(path); err == nil {
				log.Infof("sentinel file detected -> re-provisioning")
				_ = os.Remove(path)
				fire()
			}
		}
	}
}

// WatchGPIO polls a GPIO line via the libgpiod "gpioget" tool. When the button
// is held for `hold`, fire() runs. Disabled if chip is empty or gpioget missing.
func WatchGPIO(ctx context.Context, chip string, line int, activeLow bool, hold time.Duration, fire func(), log *logx.Logger) {
	if chip == "" {
		return
	}
	if _, err := exec.LookPath("gpioget"); err != nil {
		log.Warnf("GPIO trigger requested but 'gpioget' (libgpiod) not found; disabled")
		return
	}
	log.Infof("watching GPIO button on %s line %d (hold %s)", chip, line, hold)

	pressedVal := "1"
	if activeLow {
		pressedVal = "0"
	}

	const poll = 200 * time.Millisecond
	var heldFor time.Duration
	armed := true // require a release before firing again

	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			val, err := readGPIO(ctx, chip, line)
			if err != nil {
				log.Debugf("gpioget: %v", err)
				continue
			}
			if val == pressedVal {
				heldFor += poll
				if armed && heldFor >= hold {
					log.Infof("GPIO button held -> re-provisioning")
					fire()
					armed = false
				}
			} else {
				heldFor = 0
				armed = true
			}
		}
	}
}

func readGPIO(ctx context.Context, chip string, line int) (string, error) {
	out, err := exec.CommandContext(ctx, "gpioget", chip, strconv.Itoa(line)).Output()
	if err != nil {
		return "", err
	}
	// gpioget prints e.g. "0" or "1" (some versions "\"line\"=0").
	s := strings.TrimSpace(string(out))
	if i := strings.LastIndexAny(s, "01"); i >= 0 {
		return string(s[i]), nil
	}
	return s, nil
}
