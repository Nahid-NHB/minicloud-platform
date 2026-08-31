// Package dns implements the in-cluster service-discovery DNS server.
//
// The server is authoritative for "<service>.<project>.svc.cluster.local"
// and forwards everything else to an upstream resolver. Records live in
// the platform state store; the DNS server watches the store and
// re-renders the in-memory zone on every change.
//
// A simple UDP and TCP listener handles queries; the wire format is
// implemented from scratch to keep the dependency footprint small.
package dns

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/minicloud/platform/internal/state"
)

// Server is the DNS server.
type Server struct {
	store   *state.Store
	udp     *net.UDPConn
	tcp     net.Listener
	zone    string
	upstream string

	mu       sync.RWMutex
	zoneData map[string]zoneEntry // name -> entry

	stopCh chan struct{}
}

type zoneEntry struct {
	Records []state.DNSRecord
}

// Config configures the server.
type Config struct {
	Store    *state.Store
	Listen   string // e.g. ":53"
	Zone     string // base zone suffix
	Upstream string // upstream resolver, e.g. "1.1.1.1:53" (optional)
}

// NewServer creates a new DNS server.
func NewServer(cfg Config) *Server {
	if cfg.Zone == "" {
		cfg.Zone = "cluster.local"
	}
	return &Server{
		store:    cfg.Store,
		zone:     cfg.Zone,
		upstream: cfg.Upstream,
		zoneData: map[string]zoneEntry{},
		stopCh:   make(chan struct{}),
	}
}

// Run starts the listeners and the zone refresh loop.
func (s *Server) Run(ctx context.Context) error {
	udpAddr, err := net.ResolveUDPAddr("udp", s.cfg().Listen)
	if err != nil {
		return err
	}
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	s.udp = udp
	tcpAddr, err := net.ResolveTCPAddr("tcp", s.cfg().Listen)
	if err != nil {
		return err
	}
	tcp, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	s.tcp = tcp

	// Prime the zone.
	if err := s.refreshZone(ctx); err != nil {
		return err
	}

	go s.serveUDP(ctx)
	go s.serveTCP(ctx)
	go s.zoneRefresher(ctx)
	<-ctx.Done()
	udp.Close()
	tcp.Close()
	close(s.stopCh)
	return nil
}

func (s *Server) cfg() Config {
	return Config{Store: s.store, Listen: addr(s.udp, s.tcp), Zone: s.zone, Upstream: s.upstream}
}

func addr(u *net.UDPConn, t net.Listener) string {
	if u != nil {
		return u.LocalAddr().String()
	}
	if t != nil {
		return t.Addr().String()
	}
	return ""
}

func (s *Server) serveUDP(ctx context.Context) {
	for {
		buf := make([]byte, 1500)
		_ = s.udp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, src, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			default:
				continue
			}
		}
		resp, err := s.handleQuery(buf[:n])
		if err != nil {
			continue
		}
		_, _ = s.udp.WriteToUDP(resp, src)
	}
}

func (s *Server) serveTCP(ctx context.Context) {
	for {
		_ = s.tcp.(*net.TCPListener).SetDeadline(time.Now().Add(500 * time.Millisecond))
		conn, err := s.tcp.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			default:
				continue
			}
		}
		go func(c net.Conn) {
			defer c.Close()
			ln := make([]byte, 2)
			if _, err := io.ReadFull(c, ln); err != nil {
				return
			}
			l := binary.BigEndian.Uint16(ln)
			body := make([]byte, l)
			if _, err := io.ReadFull(c, body); err != nil {
				return
			}
			resp, err := s.handleQuery(body)
			if err != nil {
				return
			}
			out := make([]byte, 2)
			binary.BigEndian.PutUint16(out, uint16(len(resp)))
			_, _ = c.Write(out)
			_, _ = c.Write(resp)
		}(conn)
	}
}

func (s *Server) zoneRefresher(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			_ = s.refreshZone(ctx)
		}
	}
}

func (s *Server) refreshZone(ctx context.Context) error {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	newZone := map[string]zoneEntry{}
	for _, p := range projects {
		rs, _ := s.store.ListDNS(ctx, p.ID)
		for _, r := range rs {
			full := strings.ToLower(r.Name)
			newZone[full] = zoneEntry{Records: append(newZone[full].Records, *r)}
		}
	}
	s.mu.Lock()
	s.zoneData = newZone
	s.mu.Unlock()
	return nil
}

// handleQuery parses a DNS query and returns a response. We support
// the A record type only; everything else returns NXDOMAIN.
func (s *Server) handleQuery(buf []byte) ([]byte, error) {
	if len(buf) < 12 {
		return nil, errors.New("dns: short query")
	}
	// Parse question section.
	off := 12
	qname, off, err := readName(buf, off)
	if err != nil {
		return nil, err
	}
	if off+4 > len(buf) {
		return nil, errors.New("dns: short query")
	}
	qtype := binary.BigEndian.Uint16(buf[off : off+2])
	qclass := binary.BigEndian.Uint16(buf[off+2 : off+4])
	_ = qclass
	// Build response.
	resp := &bytes.Buffer{}
	// Header.
	hdr := buf[:12]
	hdr[2] = 0x81 // QR=1, RD=1
	hdr[3] = 0x80 // RA=1
	hdr[6] = 0
	hdr[7] = 0
	resp.Write(hdr)
	binary.Write(resp, binary.BigEndian, uint16(1))   // QDCOUNT
	binary.Write(resp, binary.BigEndian, uint16(0))   // ANCOUNT
	binary.Write(resp, binary.BigEndian, uint16(0))   // NSCOUNT
	binary.Write(resp, binary.BigEndian, uint16(0))   // ARCOUNT
	// Question.
	writeName(resp, qname)
	binary.Write(resp, binary.BigEndian, qtype)
	binary.Write(resp, binary.BigEndian, qclass)

	s.mu.RLock()
	entry, ok := s.zoneData[strings.ToLower(qname)]
	s.mu.RUnlock()
	if !ok || qtype != 1 {
		// NXDOMAIN
		hdr[3] = 0x83 // RA=1, RCODE=3
		return resp.Bytes(), nil
	}
	out := resp.Bytes()
	// Update ANCOUNT and append answers.
	anCount := uint16(len(entry.Records))
	binary.BigEndian.PutUint16(out[6:8], anCount)
	for _, r := range entry.Records {
		writeName(resp, qname)
		binary.Write(resp, binary.BigEndian, uint16(1))     // A
		binary.Write(resp, binary.BigEndian, uint16(1))     // IN
		binary.Write(resp, binary.BigEndian, uint32(60))    // TTL
		binary.Write(resp, binary.BigEndian, uint16(4))     // RDLENGTH
		ip := net.ParseIP(r.IP).To4()
		if ip == nil {
			continue
		}
		resp.Write(ip)
	}
	return resp.Bytes(), nil
}

// ---------- wire-format helpers ----------

func readName(buf []byte, off int) (string, int, error) {
	var labels []string
	for {
		if off >= len(buf) {
			return "", off, errors.New("dns: name out of bounds")
		}
		l := int(buf[off])
		off++
		if l == 0 {
			break
		}
		if l >= 192 {
			// Pointer.
			if off >= len(buf) {
				return "", off, errors.New("dns: pointer truncated")
			}
			ptr := int(binary.BigEndian.Uint16(buf[off-1:off+1])) & 0x3FFF
			sub, _, err := readName(buf, ptr)
			if err != nil {
				return "", off, err
			}
			labels = append(labels, sub)
			off++
			return strings.Join(labels, "."), off, nil
		}
		if off+l > len(buf) {
			return "", off, errors.New("dns: label out of bounds")
		}
		labels = append(labels, string(buf[off:off+l]))
		off += l
	}
	return strings.Join(labels, "."), off, nil
}

func writeName(w io.Writer, name string) {
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		_, _ = w.Write([]byte{byte(len(label))})
		_, _ = w.Write([]byte(label))
	}
	_, _ = w.Write([]byte{0})
}

// UpdateRecord adds a record to the zone. This is a convenience
// method that the API/CLI uses to register a service.
func (s *Server) UpdateRecord(ctx context.Context, projectID, name, ip string) error {
	r := &state.DNSRecord{
		Base:      state.Base{ID: fmt.Sprintf("%s.%s", name, projectID), Name: name, ProjectID: projectID},
		IP:        ip,
		Type:      "A",
	}
	return s.store.PutDNS(ctx, r)
}
