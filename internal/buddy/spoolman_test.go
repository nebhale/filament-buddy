package buddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeInventory struct {
	mu     sync.Mutex
	used   map[int]int64
	mode   string
	writes int
}

func (f *fakeInventory) serve(rw http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 {
		out := []any{}
		for id, n := range f.used {
			out = append(out, map[string]any{"id": id, "used_weight": float64(n) / 1000, "archived": id == 2, "filament": map[string]any{"name": "Purple PLA", "material": "PLA", "color_hex": "cc99ff"}})
		}
		json.NewEncoder(rw).Encode(out)
		return
	}
	id, _ := strconv.Atoi(parts[3])
	before, ok := f.used[id]
	if !ok {
		http.Error(rw, "missing", 404)
		return
	}
	if f.mode == "offline" {
		http.Error(rw, "offline", 503)
		return
	}
	if r.Method == "PUT" {
		var body struct {
			Weight float64 `json:"use_weight"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.used[id] = max(0, before+int64(body.Weight*1000))
		f.writes++
		if f.mode == "ambiguous" {
			http.Error(rw, "committed then failed", 500)
			return
		}
		if f.mode == "malformed" {
			rw.Write([]byte("{"))
			return
		}
	}
	json.NewEncoder(rw).Encode(map[string]any{"id": id, "used_weight": float64(f.used[id]) / 1000, "filament": map[string]any{"name": "Purple PLA"}})
}
func fakeClient(t *testing.T, s *Store) (*Spoolman, *fakeInventory) {
	t.Helper()
	f := &fakeInventory{used: map[int]int64{1: 100000, 2: 200000, 3: 0}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	c := testConfig()
	c.Spoolman.URL = server.URL
	return NewSpoolman(c, s), f
}
func completed(t *testing.T, s *Store) (string, string) {
	t.Helper()
	at := time.Now().Add(-30 * 24 * time.Hour)
	id := apply(t, s, "M118 FB1 START c1 A month ago", at)
	apply(t, s, "M118 FB1 STOP c1 1 12000", at.Add(time.Hour))
	return id, get(t, s, id).Sections[0].ID
}
func drain(t *testing.T, c *Spoolman) {
	t.Helper()
	for range 20 {
		did, err := c.SyncOne(context.Background())
		must(t, err)
		if !did {
			return
		}
	}
	t.Fatal("sync did not settle")
}
func TestAccountingTransfersAndOverrides(t *testing.T) {
	s := testStore(t)
	c, f := fakeClient(t, s)
	id, sid := completed(t, s)
	must(t, c.Refresh(context.Background()))
	spools, err := s.Spools()
	must(t, err)
	if len(spools) != 3 {
		t.Fatal("catalog incomplete")
	}
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	drain(t, c)
	if f.used[1] != 112000 {
		t.Fatal(f.used)
	}
	writes := f.writes
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	drain(t, c)
	if f.writes != writes {
		t.Fatal("same assignment charged twice")
	}
	// An unrelated later consumption must survive every correction.
	f.used[1] += 5000
	must(t, s.SetSection(id, sid, -1, 2, nil, false))
	drain(t, c)
	if f.used[1] != 105000 || f.used[2] != 212000 {
		t.Fatal(f.used)
	}
	must(t, s.SetSection(id, sid, -1, 0, nil, false))
	drain(t, c)
	if f.used[2] != 200000 {
		t.Fatal(f.used)
	}
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	drain(t, c)
	if f.used[1] != 117000 {
		t.Fatal(f.used)
	}
	must(t, s.SetSection(id, sid, -1, 0, ptr(10000), true))
	drain(t, c)
	if f.used[1] != 115000 {
		t.Fatal(f.used)
	}
	ss := get(t, s, id)
	if *ss.Sections[0].ReportedMG != 12000 {
		t.Fatal("override destroyed report")
	}
	must(t, s.SetSection(id, sid, -1, 0, nil, true))
	drain(t, c)
	if f.used[1] != 117000 {
		t.Fatal(f.used)
	}
	must(t, s.Archive(id, -1, true))
	must(t, s.SetSection(id, sid, -1, 0, nil, false))
	if get(t, s, id).Archived {
		t.Fatal("clearing the spool did not restore the session")
	}
	drain(t, c)
	if f.used[1] != 105000 {
		t.Fatal("archived edit failed")
	}
}
func TestUncertainDoesNotReplay(t *testing.T) {
	for _, mode := range []string{"ambiguous", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			c, f := fakeClient(t, s)
			id, sid := completed(t, s)
			f.mode = mode
			must(t, s.SetSection(id, sid, -1, 1, nil, false))
			drain(t, c)
			if f.writes != 1 || f.used[1] != 112000 {
				t.Fatal(f.used)
			}
			ss := get(t, s, id)
			op := ss.Sections[0].Operations[0]
			if op.State != "uncertain" {
				t.Fatal(op)
			}
			f.mode = ""
			drain(t, c)
			if f.writes != 1 {
				t.Fatal("uncertain request replayed")
			}
			must(t, s.Resolve(id, sid, op.ID, "applied", -1))
			drain(t, c)
			if f.writes != 1 || get(t, s, id).Sections[0].SyncStatus() != "synced" {
				t.Fatal("resolution replayed successful request")
			}
		})
	}
}
func TestConflictAndOffline(t *testing.T) {
	s := testStore(t)
	c, f := fakeClient(t, s)
	id, sid := completed(t, s)
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	f.mode = "offline"
	drain(t, c)
	ss := get(t, s, id)
	if ss.Sections[0].Operations[0].State != "pending" || f.writes != 0 {
		t.Fatal("GET failure was ambiguous")
	}
	f.mode = ""
	must(t, s.Edit(id, -1, func(ss *Session) error { ss.Sections[0].Operations[0].NextAttempt = time.Time{}; return nil }))
	drain(t, c)
	f.used[1] = 1000
	must(t, s.SetSection(id, sid, -1, 2, nil, false))
	drain(t, c)
	ss = get(t, s, id)
	var conflict Operation
	for _, op := range ss.Sections[0].Operations {
		if op.State == "conflict" {
			conflict = op
		}
	}
	if conflict.ID == "" || f.used[2] != 200000 || f.used[1] != 1000 {
		t.Fatal("underflow or replacement charge applied")
	}
	f.used[1] = 12000
	must(t, s.Resolve(id, sid, conflict.ID, "retry", -1))
	drain(t, c)
	if f.used[1] != 0 || f.used[2] != 212000 {
		t.Fatal(f.used)
	}
	must(t, s.SetSection(id, sid, -1, 999, nil, false))
	drain(t, c)
	ss = get(t, s, id)
	if ss.Sections[0].SyncStatus() != "conflict" {
		t.Fatal("deleted spool not surfaced")
	}
}
func TestCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	must(t, err)
	id, sid := completed(t, s)
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	w, err := s.Claim(time.Now())
	must(t, err)
	if w == nil {
		t.Fatal("no work")
	}
	must(t, s.Close())
	s, err = OpenStore(dir)
	must(t, err)
	defer s.Close()
	ss := get(t, s, id)
	if ss.Sections[0].SyncStatus() != "uncertain" {
		t.Fatal("inflight request requeued")
	}
	must(t, s.Resolve(id, sid, w.Operation.ID, "not-applied", -1))
	w2, err := s.Claim(time.Now())
	must(t, err)
	if w2 == nil || w2.Operation.ID == w.Operation.ID {
		t.Fatal("resolution did not create a new audited attempt")
	}
}
func TestConcurrentEdits(t *testing.T) {
	s := testStore(t)
	id, sid := completed(t, s)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := s.SetSection(id, sid, -1, n%3, nil, false); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	ss := get(t, s, id)
	var pending int64
	for _, op := range ss.Sections[0].Operations {
		if op.State == "pending" {
			pending += op.DeltaMG
		}
	}
	want := int64(0)
	if ss.Sections[0].SpoolID != 0 {
		want = 12000
	}
	if pending != want {
		t.Fatal(fmt.Sprintf("pending %d want %d", pending, want))
	}
}
