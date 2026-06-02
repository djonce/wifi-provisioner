// Package detect decides whether the device currently has working internet
// access. It is intentionally conservative: a captive-portal-style 200 is NOT
// treated as online; we require a real 204 from a known generate_204 endpoint
// or a successful TCP connection to a public resolver.
package detect

import (
	"net"
	"net/http"
	"time"
)

// IsOnline reports whether any connectivity probe succeeds within timeout.
func IsOnline(urls []string, timeout time.Duration) bool {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // do not follow captive-portal redirects
		},
	}
	for _, u := range urls {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent { // 204 == genuinely online
			return true
		}
	}
	// Fallback: raw TCP to public DNS servers (works even if HTTP probes are blocked).
	d := net.Dialer{Timeout: timeout}
	for _, addr := range []string{"223.5.5.5:53", "1.1.1.1:53", "8.8.8.8:53"} {
		c, err := d.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return true
		}
	}
	return false
}
