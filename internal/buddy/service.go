package buddy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"
)

type PrinterStatus struct {
	Printer    Printer
	Suppressed bool
	LastEvent  time.Time
	LastError  string
	Dropped    uint64
	Active     *Session
	RecordedMG int64
}
type Service struct {
	Config   Config
	Store    *Store
	Spoolman *Spoolman
	mu       sync.Mutex
	status   map[string]*PrinterStatus
}

func NewService(c Config, s *Store) *Service {
	v := &Service{Config: c, Store: s, Spoolman: NewSpoolman(c, s), status: map[string]*PrinterStatus{}}
	for _, p := range c.Printers {
		v.status[p.ID] = &PrinterStatus{Printer: p}
	}
	return v
}
func (s *Service) Handle(e Event) error {
	defer s.Store.changes.publish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.status[e.PrinterID]
	if !ok {
		return errors.New("unknown printer")
	}
	if p.Printer.SourceIP != "" {
		a, err := netip.ParseAddr(e.SourceIP)
		b, _ := netip.ParseAddr(p.Printer.SourceIP)
		if err != nil || a.Unmap() != b.Unmap() {
			return errors.New("unexpected sender")
		}
	}
	p.LastEvent = e.ReceivedAt
	if p.Suppressed && e.Kind == "CHANGE" {
		return nil
	}
	result, err := s.Store.Apply(e)
	if err != nil {
		p.LastError = err.Error()
		return err
	}
	if e.Kind == "START" {
		p.Suppressed = false
	}
	slog.Info("printer marker", "printer", e.PrinterID, "kind", e.Kind, "section", e.Section, "result", result)
	p.LastError = ""
	if result == "orphan end" || result == "conflicting marker" || result == "out-of-order end" {
		p.LastError = result
	}
	return nil
}
func (s *Service) CloseSession(id string, rev int) error {
	defer s.Store.changes.publish()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.Store.Get(id)
	if err != nil {
		return err
	}
	if err = s.Store.CloseSession(id, rev); err != nil {
		return err
	}
	if p := s.status[ss.PrinterID]; p != nil {
		p.Suppressed = true
	}
	return nil
}
func (s *Service) Status() ([]PrinterStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.Store.list(`SELECT body FROM sessions WHERE state='active'`)
	if err != nil {
		return nil, err
	}
	out := []PrinterStatus{}
	for _, p := range s.Config.Printers {
		v := *s.status[p.ID]
		for _, ss := range active {
			if ss.PrinterID == p.ID {
				v.Active = &ss
				v.RecordedMG = ss.TotalMG()
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) ServeUDP(ctx context.Context, conn *net.UDPConn) error {
	buf := make([]byte, 65535)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return err
		}
		for _, e := range ParsePacket(buf[:n], addr.IP.String(), time.Now()) {
			if err = s.Handle(e); err != nil {
				slog.Warn("printer marker", "printer", e.PrinterID, "error", err)
			}
		}
	}
}
