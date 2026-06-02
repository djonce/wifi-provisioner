package portal

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"wifi-provisioner/internal/logx"
)

func newTestDHCP() *DHCPServer {
	return NewDHCPServer("wlan0",
		net.ParseIP("192.168.4.1"), net.CIDRMask(24, 32),
		net.ParseIP("192.168.4.50"), net.ParseIP("192.168.4.150"),
		10*time.Minute, logx.New(false))
}

// buildDiscover crafts a minimal valid DHCPDISCOVER packet.
func buildDiscover(mac net.HardwareAddr) []byte {
	b := make([]byte, 244)
	b[0] = 1 // BOOTREQUEST
	b[1] = 1 // ethernet
	b[2] = 6 // hlen
	binary.BigEndian.PutUint32(b[4:8], 0x12345678)
	binary.BigEndian.PutUint16(b[10:12], 0x8000) // broadcast flag
	copy(b[28:34], mac)
	b[236], b[237], b[238], b[239] = 99, 130, 83, 99
	// options: 53=DISCOVER, end
	b[240], b[241], b[242] = 53, 1, 1
	b[243] = 255
	return b
}

func optionsOf(reply []byte) map[byte][]byte {
	opts := map[byte][]byte{}
	i := 240
	for i < len(reply) {
		c := reply[i]
		i++
		if c == 0 {
			continue
		}
		if c == 255 {
			break
		}
		l := int(reply[i])
		i++
		opts[c] = reply[i : i+l]
		i += l
	}
	return opts
}

func TestDHCPDiscoverOffer(t *testing.T) {
	s := newTestDHCP()
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	pkt, err := parseDHCP(buildDiscover(mac))
	if err != nil {
		t.Fatalf("parseDHCP: %v", err)
	}
	if pkt.msgType() != 1 {
		t.Fatalf("want DISCOVER(1), got %d", pkt.msgType())
	}
	if pkt.mac() != mac.String() {
		t.Fatalf("mac mismatch: %s vs %s", pkt.mac(), mac)
	}

	ip := s.allocate(pkt.mac(), nil)
	if ip == nil {
		t.Fatal("allocate returned nil")
	}
	if ip2int(ip) < ip2int(net.ParseIP("192.168.4.50").To4()) ||
		ip2int(ip) > ip2int(net.ParseIP("192.168.4.150").To4()) {
		t.Fatalf("allocated IP %s outside pool", ip)
	}
	// Same MAC must get the same lease back.
	if again := s.allocate(pkt.mac(), nil); !again.Equal(ip) {
		t.Fatalf("lease not sticky: %s vs %s", again, ip)
	}

	reply := s.buildReply(pkt, 2, ip)
	if reply[0] != 2 {
		t.Fatalf("reply op should be BOOTREPLY(2), got %d", reply[0])
	}
	if binary.BigEndian.Uint32(reply[4:8]) != 0x12345678 {
		t.Fatal("xid not echoed")
	}
	if !net.IP(reply[16:20]).Equal(ip) {
		t.Fatalf("yiaddr mismatch: %s vs %s", net.IP(reply[16:20]), ip)
	}
	opts := optionsOf(reply)
	if opts[53][0] != 2 {
		t.Fatalf("message type should be OFFER(2), got %d", opts[53][0])
	}
	if !net.IP(opts[3]).Equal(net.ParseIP("192.168.4.1")) {
		t.Fatalf("router option wrong: %s", net.IP(opts[3]))
	}
	if !net.IP(opts[6]).Equal(net.ParseIP("192.168.4.1")) {
		t.Fatalf("dns option wrong: %s", net.IP(opts[6]))
	}
}

func TestDNSHijack(t *testing.T) {
	s := NewDNSServer("wlan0", net.ParseIP("192.168.4.1"), net.ParseIP("192.168.4.1"), logx.New(false))

	// Query: id=0xABCD, RD set, 1 question for "example.com" A IN.
	q := []byte{0xAB, 0xCD, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	q = append(q, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0)
	q = append(q, 0x00, 0x01, 0x00, 0x01) // qtype A, qclass IN

	resp := s.reply(q)
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp[0] != 0xAB || resp[1] != 0xCD {
		t.Fatal("transaction id not echoed")
	}
	if resp[2]&0x80 == 0 {
		t.Fatal("QR bit not set")
	}
	if an := binary.BigEndian.Uint16(resp[6:8]); an != 1 {
		t.Fatalf("want ANCOUNT 1, got %d", an)
	}
	// Last 4 bytes are the A record address.
	got := net.IP(resp[len(resp)-4:])
	if !got.Equal(net.ParseIP("192.168.4.1")) {
		t.Fatalf("answer IP wrong: %s", got)
	}
}
