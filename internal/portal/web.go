package portal

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"wifi-provisioner/internal/backend"
	"wifi-provisioner/internal/logx"
)

//go:embed assets/index.html
var indexHTML []byte

// Status is reported to the phone so the UI can show progress.
type Status struct {
	State   string `json:"state"`             // provisioning | connecting | connected | failed
	SSID    string `json:"ssid,omitempty"`    // network being joined
	Message string `json:"message,omitempty"` // human readable detail
}

// Handlers are the callbacks the web server needs from the application core.
type Handlers struct {
	Networks func() []backend.Network // current scan results
	Connect  func(ssid, pass string)  // non-blocking: kick off a join attempt
	Status   func() Status            // current state
}

type Server struct {
	apIP string
	port int
	h    Handlers
	log  *logx.Logger
	srv  *http.Server
}

func NewServer(apIP net.IP, port int, h Handlers, log *logx.Logger) *Server {
	return &Server{apIP: apIP.String(), port: port, h: h, log: log}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/networks", s.handleNetworks)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/status", s.handleStatus)
	// Everything else (including OS captive-portal probe URLs such as
	// /generate_204, /hotspot-detect.html, /ncsi.txt) serves the portal page,
	// which is exactly what triggers the "sign in" browser to open.
	mux.HandleFunc("/", s.handleIndex)

	ln, err := net.Listen("tcp", net.JoinHostPort(s.apIP, strconv.Itoa(s.port)))
	if err != nil {
		return fmt.Errorf("web listen %s:%d: %w", s.apIP, s.port, err)
	}
	s.srv = &http.Server{Handler: mux}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Errorf("web server: %v", err)
		}
	}()
	s.log.Infof("captive portal listening on http://%s:%d", s.apIP, s.port)
	return nil
}

func (s *Server) Stop() {
	if s.srv != nil {
		s.srv.Close()
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(indexHTML)
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.h.Networks())
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.h.Status())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ssid, pass := parseCredentials(r)
	if strings.TrimSpace(ssid) == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "SSID is required"})
		return
	}
	s.h.Connect(ssid, pass)
	writeJSON(w, map[string]any{"ok": true})
}

func parseCredentials(r *http.Request) (ssid, pass string) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			SSID     string `json:"ssid"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			return body.SSID, body.Password
		}
		return "", ""
	}
	_ = r.ParseForm()
	return r.FormValue("ssid"), r.FormValue("password")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
