package portal

import (
	"encoding/binary"
	"fmt"
	"net"

	"wifi-provisioner/internal/logx"
)

// DNSServer answers every A query with the portal IP. This "DNS hijack" is what
// makes phones pop up the "Sign in to network" captive-portal notification: the
// OS connectivity-check hostname resolves to us and gets a non-expected reply.
type DNSServer struct {
	iface    string
	listenIP net.IP
	answerIP net.IP
	log      *logx.Logger
	conn     *net.UDPConn
}

func NewDNSServer(iface string, listenIP, answerIP net.IP, log *logx.Logger) *DNSServer {
	return &DNSServer{iface: iface, listenIP: listenIP.To4(), answerIP: answerIP.To4(), log: log}
}

func (s *DNSServer) Start() error {
	conn, err := listenUDP4(s.iface, fmt.Sprintf("%s:53", s.listenIP), false)
	if err != nil {
		return fmt.Errorf("dns listen: %w", err)
	}
	s.conn = conn
	go s.serve()
	s.log.Infof("DNS hijack listening on %s:53 -> %s", s.listenIP, s.answerIP)
	return nil
}

func (s *DNSServer) Stop() {
	if s.conn != nil {
		s.conn.Close()
	}
}

func (s *DNSServer) serve() {
	buf := make([]byte, 1500)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		resp := s.reply(buf[:n])
		if resp != nil {
			if _, err := s.conn.WriteToUDP(resp, addr); err != nil {
				s.log.Debugf("dns send: %v", err)
			}
		}
	}
}

// reply builds a response that maps any A query to answerIP. Non-A queries get
// an empty (NOERROR) answer so the client falls back to the A record.
func (s *DNSServer) reply(req []byte) []byte {
	if len(req) < 12 {
		return nil
	}
	if req[2]&0x80 != 0 { // already a response
		return nil
	}
	if req[2]&0x78 != 0 { // non-standard opcode
		return nil
	}
	qd := binary.BigEndian.Uint16(req[4:6])
	if qd < 1 {
		return nil
	}

	// Walk the (first) question's name to find qtype.
	off := 12
	for off < len(req) {
		l := int(req[off])
		if l == 0 {
			off++
			break
		}
		if l&0xC0 == 0xC0 { // compression pointer (unexpected in a question)
			off += 2
			break
		}
		off += l + 1
	}
	if off+4 > len(req) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(req[off : off+2])
	qend := off + 4 // qtype(2) + qclass(2)

	resp := make([]byte, 0, len(req)+16)
	resp = append(resp, req[0], req[1]) // transaction id
	resp = append(resp, 0x81, 0x80)     // QR=1, RD=1, RA=1
	resp = append(resp, 0x00, 0x01)     // QDCOUNT=1
	if qtype == 1 {                     // A
		resp = append(resp, 0x00, 0x01) // ANCOUNT=1
	} else {
		resp = append(resp, 0x00, 0x00) // ANCOUNT=0
	}
	resp = append(resp, 0x00, 0x00, 0x00, 0x00) // NSCOUNT, ARCOUNT
	resp = append(resp, req[12:qend]...)        // echo the question

	if qtype == 1 {
		resp = append(resp, 0xC0, 0x0C)             // name pointer to offset 12
		resp = append(resp, 0x00, 0x01)             // type A
		resp = append(resp, 0x00, 0x01)             // class IN
		resp = append(resp, 0x00, 0x00, 0x00, 0x1E) // TTL 30s
		resp = append(resp, 0x00, 0x04)             // rdlength
		resp = append(resp, s.answerIP...)
	}
	return resp
}
