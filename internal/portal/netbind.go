package portal

import (
	"context"
	"net"
	"syscall"
)

// listenUDP4 opens a UDP socket bound to a specific interface (SO_BINDTODEVICE)
// so our DHCP/DNS responders only ever answer on the hotspot, never on a real
// uplink. broadcast enables sending to 255.255.255.255 (needed for DHCP).
func listenUDP4(iface, laddr string, broadcast bool) (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if cerr := c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				if broadcast {
					_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
				}
				if iface != "" {
					serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
				}
			}); cerr != nil {
				return cerr
			}
			return serr
		},
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", laddr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func ip2int(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func int2ip(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
