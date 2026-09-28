package buddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type remoteSpool struct {
	ID        int      `json:"id"`
	Archived  bool     `json:"archived"`
	Used      float64  `json:"used_weight"`
	Remaining *float64 `json:"remaining_weight"`
	Filament  struct {
		Name     string `json:"name"`
		Material string `json:"material"`
		Color    string `json:"color_hex"`
		Vendor   *struct {
			Name string `json:"name"`
		} `json:"vendor"`
	} `json:"filament"`
}

func (v remoteSpool) spool() Spool {
	label := v.Filament.Name
	if v.Filament.Vendor != nil {
		label = v.Filament.Vendor.Name + " " + label
	}
	label = fmt.Sprintf("#%d · %s · %s", v.ID, strings.TrimSpace(label), v.Filament.Material)
	s := Spool{ID: v.ID, Label: label, Color: v.Filament.Color, Archived: v.Archived, UsedMG: int64(math.Round(v.Used * 1000)), UpdatedAt: time.Now().UTC()}
	if v.Remaining != nil {
		mg := int64(math.Round(*v.Remaining * 1000))
		s.RemainingMG = &mg
	}
	return s
}

type Spoolman struct {
	base        string
	client      *http.Client
	store       *Store
	mu          sync.Mutex
	lastError   string
	refreshedAt time.Time
	refreshing  bool
	refreshMu   sync.Mutex
}

func NewSpoolman(c Config, s *Store) *Spoolman {
	return &Spoolman{base: strings.TrimRight(c.Spoolman.URL, "/"), client: &http.Client{Timeout: c.Spoolman.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, store: s}
}
func (c *Spoolman) Status() string         { c.mu.Lock(); defer c.mu.Unlock(); return c.lastError }
func (c *Spoolman) RefreshedAt() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.refreshedAt }
func (c *Spoolman) Refreshing() bool       { c.mu.Lock(); defer c.mu.Unlock(); return c.refreshing }
func (c *Spoolman) status(err error) {
	defer c.store.changes.publish()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastError = ""
	if err != nil {
		c.lastError = err.Error()
	}
}

// Errors deliberately omit request URLs, which may contain upstream credentials.
func (c *Spoolman) request(ctx context.Context, method, path string, body any, out any) (int, error) {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return 0, errors.New("invalid Spoolman request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, errors.New("Spoolman request interrupted or unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("Spoolman returned HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return resp.StatusCode, errors.New("Spoolman returned an invalid response")
	}
	return resp.StatusCode, nil
}
func (c *Spoolman) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	c.refreshing = true
	c.mu.Unlock()
	c.store.changes.publish()
	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
		c.store.changes.publish()
	}()
	// Explicitly include archived spools; no cache deletion, so history survives
	// spool deletion. Assignment GET validates an uncached or deleted choice.
	var out []Spool
	for offset := 0; ; offset += 100 {
		var page []remoteSpool
		_, err := c.request(ctx, "GET", "/api/v1/spool?limit=100&offset="+strconv.Itoa(offset)+"&allow_archived=true", nil, &page)
		if err != nil {
			c.status(err)
			return err
		}
		for _, v := range page {
			out = append(out, v.spool())
		}
		if len(page) < 100 {
			break
		}
		if offset >= 100000 {
			return errors.New("Spoolman inventory exceeds supported pagination")
		}
	}
	err := c.store.CacheSpools(out)
	if err == nil {
		c.mu.Lock()
		c.refreshedAt = time.Now().UTC()
		c.mu.Unlock()
	}
	c.status(err)
	return err
}
func (c *Spoolman) Get(ctx context.Context, id int) (Spool, int, error) {
	var v remoteSpool
	status, err := c.request(ctx, "GET", "/api/v1/spool/"+strconv.Itoa(id), nil, &v)
	if err == nil && (v.ID != id || math.IsNaN(v.Used) || math.IsInf(v.Used, 0) || v.Used < 0) {
		err = errors.New("Spoolman returned invalid spool accounting")
	}
	return v.spool(), status, err
}

type Work struct {
	SessionID string
	SectionID string
	Operation Operation
}

func (s *Store) Claim(now time.Time) (*Work, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.list(`SELECT body FROM sessions WHERE work=1 ORDER BY opened`)
	if err != nil {
		return nil, err
	}
	blocked := map[int]bool{}
	for _, ss := range sessions {
		for _, v := range ss.Sections {
			for _, op := range v.Operations {
				if op.State == "uncertain" || op.State == "conflict" || op.State == "inflight" {
					blocked[op.SpoolID] = true
				}
			}
		}
	}
	for _, ss := range sessions {
		for i := range ss.Sections {
			v := &ss.Sections[i]
			frozen := false
			for _, op := range v.Operations {
				if op.State == "uncertain" || op.State == "conflict" || op.State == "inflight" {
					frozen = true
				}
			}
			if frozen {
				continue
			}
			for j := range v.Operations {
				op := &v.Operations[j]
				if op.State != "pending" {
					continue
				} // preserve refund-before-charge order even during backoff
				if blocked[op.SpoolID] || op.NextAttempt.After(now) {
					break
				}
				op.State = "inflight"
				op.Attempts++
				op.UpdatedAt = now
				w := &Work{ss.ID, v.ID, *op}
				if err = s.save(s.db, &ss); err != nil {
					return nil, err
				}
				s.changes.publish()
				return w, nil
			}
		}
	}
	return nil, nil
}
func (s *Store) Finish(w Work, state, message string, before *int64) error {
	return s.Edit(w.SessionID, -1, func(ss *Session) error {
		v, err := section(ss, w.SectionID)
		if err != nil {
			return err
		}
		for i := range v.Operations {
			op := &v.Operations[i]
			if op.ID != w.Operation.ID {
				continue
			}
			if op.State != "inflight" {
				return ErrConflict
			}
			op.State = state
			op.Error = message
			op.UpdatedAt = time.Now().UTC()
			if before != nil {
				op.BeforeMG = before
			}
			if state == "pending" {
				op.NextAttempt = time.Now().Add(time.Duration(min(300, 1<<min(op.Attempts, 8))) * time.Second)
			}
			if state == "applied" {
				v.Applied[op.SpoolID] += op.DeltaMG
			}
			if state != "inflight" {
				ss.audit("Spoolman "+state, fmt.Sprintf("%s: spool %d, %+.3f g. %s", op.ID, op.SpoolID, float64(op.DeltaMG)/1000, message))
			}
			return nil
		}
		return ErrNotFound
	})
}
func (s *Store) Resolve(id, sid, oid, action string, rev int) error {
	return s.Edit(id, rev, func(ss *Session) error {
		v, err := section(ss, sid)
		if err != nil {
			return err
		}
		for i := range v.Operations {
			op := &v.Operations[i]
			if op.ID != oid {
				continue
			}
			switch action {
			case "applied", "not-applied":
				if op.State != "uncertain" {
					return ErrConflict
				}
				if action == "applied" {
					v.Applied[op.SpoolID] += op.DeltaMG
					op.State = "applied"
				} else {
					op.State = "not-applied"
				}
			case "retry":
				if op.State != "conflict" {
					return ErrConflict
				}
				op.State = "not-applied"
			default:
				return ErrConflict
			}
			op.UpdatedAt = time.Now().UTC()
			ss.audit("manual reconciliation", oid+": "+action)
			return nil
		}
		return ErrNotFound
	})
}
func (c *Spoolman) SyncOne(ctx context.Context) (bool, error) {
	w, err := c.store.Claim(time.Now().UTC())
	if err != nil || w == nil {
		return false, err
	}
	before, status, err := c.Get(ctx, w.Operation.SpoolID)
	if err != nil {
		state := "pending"
		if status == 404 || status == 400 || status == 401 || status == 403 {
			state = "conflict"
		}
		return true, c.store.Finish(*w, state, err.Error(), nil)
	}
	if w.Operation.DeltaMG < 0 && before.UsedMG < -w.Operation.DeltaMG {
		return true, c.store.Finish(*w, "conflict", "Refund exceeds Spoolman's recorded used weight. Correct the external discrepancy before retrying.", &before.UsedMG)
	}
	if err = c.store.Finish(*w, "inflight", "", &before.UsedMG); err != nil {
		return true, err
	}
	var result remoteSpool
	status, err = c.request(ctx, "PUT", "/api/v1/spool/"+strconv.Itoa(w.Operation.SpoolID)+"/use", map[string]float64{"use_weight": float64(w.Operation.DeltaMG) / 1000}, &result)
	if err != nil {
		state := "uncertain"
		if status == 400 || status == 404 || status == 401 || status == 403 || status == 422 {
			state = "conflict"
		}
		return true, c.store.Finish(*w, state, err.Error(), nil)
	}
	after := result.spool()
	if result.ID != w.Operation.SpoolID || math.IsNaN(result.Used) || math.IsInf(result.Used, 0) || after.UsedMG != before.UsedMG+w.Operation.DeltaMG {
		return true, c.store.Finish(*w, "uncertain", "Returned balance differed from the expected adjustment; inspect concurrent edits or a clamped refund.", nil)
	}
	if err = c.store.Finish(*w, "applied", "", nil); err != nil {
		return true, err
	}
	return true, c.store.CacheSpools([]Spool{after})
}
func (c *Spoolman) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for range 50 {
				did, err := c.SyncOne(ctx)
				if err != nil {
					slog.Error("spool synchronization", "error", err)
					break
				}
				if !did || ctx.Err() != nil {
					break
				}
			}
		}
	}
}
func (c *Spoolman) RefreshLoop(ctx context.Context) {
	for {
		if err := c.Refresh(ctx); err != nil {
			slog.Warn("spool catalog", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}
