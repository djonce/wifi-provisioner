package portal

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"wifi-provisioner/internal/logx"
)

const (
	dhcpServerPort = 67
	dhcpClientPort = 68
)

// DHCPServer is a minimal DHCPv4 server for the captive portal. It hands out a
// small pool, and advertises itself as both router and DNS so that all client
// traffic and name resolution is funnelled to the portal.
type DHCPServer struct {
	iface     string
	serverIP  net.IP
	mask      net.IPMask
	poolStart net.IP
	poolEnd   net.IP
	lease     time.Duration
	log       *logx.Logger

	mu     sync.Mutex
	leases map[string]net.IP // client MAC -> assigned IP
	conn   *net.UDPConn
}

func NewDHCPServer(iface string, serverIP net.IP, mask net.IPMask, start, end net.IP, lease time.Duration, log *logx.Logger) *DHCPServer {
	return &DHCPServer{
		iface:     iface,
		serverIP:  serverIP.To4(),
		mask:      mask,
		poolStart: start.To4(),
		poolEnd:   end.To4(),
		lease:     lease,
		log:       log,
		leases:    map[string]net.IP{},
	}
}

func (s *DHCPServer) Start() error {
	conn, err := listenUDP4(s.iface, fmt.Sprintf("0.0.0.0:%d", dhcpServerPort), true)
	if err != nil {
		return fmt.Errorf("dhcp listen: %w", err)
	}
	s.conn = conn
	go s.serve()
	s.log.Infof("DHCP server listening on %s (pool %s-%s)", s.iface, s.poolStart, s.poolEnd)
	return nil
}

func (s *DHCPServer) Stop() {
	if s.conn != nil {
		s.conn.Close()
	}
}

func (s *DHCPServer) serve() {
	buf := make([]byte, 1500)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed
		}
		pkt, err := parseDHCP(buf[:n])
		if err != nil || pkt.op != 1 { // only BOOTREQUEST
			continue
		}
		mt := pkt.msgType()
		var reply []byte
		switch mt {
		case 1: // DISCOVER
			ip := s.allocate(pkt.mac(), nil)
			if ip == nil {
				continue
			}
			reply = s.buildReply(pkt, 2, ip) // OFFER
		case 3: // REQUEST
			ip := s.allocate(pkt.mac(), pkt.requestedIP())
			if ip == nil {
				continue
			}
			reply = s.buildReply(pkt, 5, ip) // ACK
		default:
			continue
		}
		dst := &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpClientPort}
		if _, err := s.conn.WriteToUDP(reply, dst); err != nil {
			s.log.Debugf("dhcp send: %v", err)
		}
	}
}

func (s *DHCPServer) allocate(mac string, requested net.IP) net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ip, ok := s.leases[mac]; ok {
		return ip
	}
	used := map[string]bool{}
	for _, ip := range s.leases {
		used[ip.String()] = true
	}
	start, end := ip2int(s.poolStart), ip2int(s.poolEnd)
	if r := requested.To4(); r != nil {
		v := ip2int(r)
		if v >= start && v <= end && !used[r.String()] {
			s.leases[mac] = r
			return r
		}
	}
	for v := start; v <= end; v++ {
		ip := int2ip(v)
		if !used[ip.String()] {
			s.leases[mac] = ip
			return ip
		}
	}
	return nil
}

func (s *DHCPServer) buildReply(req *dhcpPacket, msgType byte, yiaddr net.IP) []byte {
	buf := make([]byte, 240)
	buf[0] = 2 // BOOTREPLY
	buf[1] = 1 // ethernet
	buf[2] = byte(len(req.chaddr))
	binary.BigEndian.PutUint32(buf[4:8], req.xid)
	binary.BigEndian.PutUint16(buf[10:12], req.flags)
	copy(buf[16:20], yiaddr.To4())     // yiaddr
	copy(buf[20:24], s.serverIP)       // siaddr (next server)
	copy(buf[24:28], req.giaddr.To4()) // giaddr
	copy(buf[28:28+len(req.chaddr)], req.chaddr)
	buf[236], buf[237], buf[238], buf[239] = 99, 130, 83, 99 // magic cookie

	leaseSecs := make([]byte, 4)
	binary.BigEndian.PutUint32(leaseSecs, uint32(s.lease/time.Second))

	var opts []byte
	add := func(code byte, data []byte) {
		opts = append(opts, code, byte(len(data)))
		opts = append(opts, data...)
	}
	add(53, []byte{msgType}) // message type
	add(54, s.serverIP)      // server identifier
	add(51, leaseSecs)       // lease time
	add(1, []byte(s.mask))   // subnet mask
	add(3, s.serverIP)       // router
	add(6, s.serverIP)       // DNS server (ourselves -> captive portal)
	opts = append(opts, 255) // end
	return append(buf, opts...)
}

// dhcpPacket is a parsed BOOTP/DHCP message (only the fields we need).
type dhcpPacket struct {
	op      byte
	xid     uint32
	flags   uint16
	giaddr  net.IP
	chaddr  net.HardwareAddr
	options map[byte][]byte
}

func (p *dhcpPacket) mac() string { return p.chaddr.String() }

func (p *dhcpPacket) msgType() byte {
	if v, ok := p.options[53]; ok && len(v) > 0 {
		return v[0]
	}
	return 0
}

func (p *dhcpPacket) requestedIP() net.IP {
	if v, ok := p.options[50]; ok && len(v) == 4 {
		return net.IP(v)
	}
	return nil
}

func parseDHCP(b []byte) (*dhcpPacket, error) {
	if len(b) < 240 {
		return nil, fmt.Errorf("dhcp packet too short")
	}
	if !(b[236] == 99 && b[237] == 130 && b[238] == 83 && b[239] == 99) {
		return nil, fmt.Errorf("bad dhcp magic cookie")
	}
	p := &dhcpPacket{
		op:      b[0],
		xid:     binary.BigEndian.Uint32(b[4:8]),
		flags:   binary.BigEndian.Uint16(b[10:12]),
		giaddr:  net.IP(append([]byte(nil), b[24:28]...)),
		options: map[byte][]byte{},
	}
	hlen := int(b[2])
	if hlen <= 0 || hlen > 16 {
		hlen = 6
	}
	p.chaddr = net.HardwareAddr(append([]byte(nil), b[28:28+hlen]...))

	i := 240
	for i < len(b) {
		code := b[i]
		i++
		if code == 0 { // pad
			continue
		}
		if code == 255 { // end
			break
		}
		if i >= len(b) {
			break
		}
		l := int(b[i])
		i++
		if i+l > len(b) {
			break
		}
		p.options[code] = append([]byte(nil), b[i:i+l]...)
		i += l
	}
	return p, nil
}
