package buddy

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func apply(t *testing.T, s *Store, raw string, at time.Time) string {
	t.Helper()
	e, err := ParseMarker(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.ReceivedAt = at
	e.SourceIP = "127.0.0.1"
	id, err := s.Apply(e)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func get(t *testing.T, s *Store, id string) Session {
	t.Helper()
	ss, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestLifecycle(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC()
	id := apply(t, s, "M118 FB1 START c1 Cube", at)
	ss := get(t, s, id)
	if ss.Current != 1 || ss.Sections[0].Weight() != nil {
		t.Fatal(ss)
	}
	must(t, s.Plan(id, -1, "add", ""))
	must(t, s.Plan(id, -1, "add", ""))
	ss = get(t, s, id)
	second := ss.Sections[1].ID
	third := ss.Sections[2].ID
	must(t, s.SetSection(id, second, -1, 42, nil, false))
	must(t, s.Plan(id, -1, "down", second))
	ss = get(t, s, id)
	if ss.Sections[2].ID != second || ss.Sections[1].ID != third {
		t.Fatal("future reorder lost identity")
	}
	must(t, s.Plan(id, -1, "up", second))
	apply(t, s, "M118 FB1 CHANGE c1 1 12000", at.Add(time.Second))
	ss = get(t, s, id)
	if ss.Sections[1].ID != second || ss.Sections[1].State != "current" || ss.TotalMG() != 12000 {
		t.Fatal(ss)
	}
	if err := s.Plan(id, -1, "remove", second); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed observed section: %v", err)
	}
	apply(t, s, "M118 FB1 STOP c1 2 5000", at.Add(2*time.Second))
	ss = get(t, s, id)
	if ss.State != "closed" || ss.TotalMG() != 17000 || ss.Sections[2].State != "unreached" {
		t.Fatal(ss)
	}
	if len(ss.Sections[1].Operations) != 1 || ss.Sections[1].Operations[0].DeltaMG != 5000 {
		t.Fatal("did not enqueue charge")
	}
	assignUnassignedSpools(t, s, id)
	must(t, s.Archive(id, -1, true))
	ss = get(t, s, id)
	if !ss.Archived {
		t.Fatal("not archived")
	}
	must(t, s.Archive(id, -1, false))
	if err := s.SetSection(id, ss.Sections[2].ID, -1, 0, ptr(100), true); !errors.Is(err, ErrConflict) {
		t.Fatal("charged unreached section")
	}
}
func ptr(n int64) *int64 { return &n }
func TestLossRecoveryAndDuplicates(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC()
	id := apply(t, s, "M118 FB1 CHANGE c1 3 5000", at)
	ss := get(t, s, id)
	if !ss.Recovered || ss.Current != 4 || ss.Sections[0].State != "missing" || ss.Sections[2].State != "complete" {
		t.Fatal(ss)
	}
	if got := apply(t, s, "M118 FB1 CHANGE c1 3 5000", at.Add(100*time.Millisecond)); got != "duplicate" {
		t.Fatal(got)
	}
	apply(t, s, "M118 FB1 CHANGE c1 1 2000", at.Add(time.Second))
	ss = get(t, s, id)
	if ss.Current != 4 || ss.TotalMG() != 7000 {
		t.Fatal(ss)
	}
	apply(t, s, "M118 FB1 CHANGE c1 1 3000", at.Add(2*time.Second))
	if get(t, s, id).TotalMG() != 7000 {
		t.Fatal("conflict replaced weight")
	}
	apply(t, s, "M118 FB1 STOP c1 1 2000", at.Add(3*time.Second))
	if get(t, s, id).State != "active" {
		t.Fatal("old end closed session")
	}
	newer := apply(t, s, "M118 FB1 START c1 Next print", at.Add(4*time.Second))
	ss = get(t, s, id)
	if newer == id || ss.CloseReason != "superseded" || ss.Sections[3].State != "incomplete" {
		t.Fatal(ss)
	}
	apply(t, s, "M118 FB1 STOP c1 1 1000", at.Add(5*time.Second))
	if got := apply(t, s, "M118 FB1 STOP c1 1 1000", at.Add(11*time.Second)); got != "orphan end" {
		t.Fatal(got)
	}
}
func TestRestartAndManualClose(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	must(t, err)
	c := testConfig()
	svc := NewService(c, s)
	at := time.Now().UTC()
	id := apply(t, s, "M118 FB1 START c1 Print", at)
	_, err = OpenStore(dir)
	if err == nil {
		t.Fatal("second process acquired lock")
	}
	must(t, svc.CloseSession(id, -1))
	e, _ := ParseMarker("M118 FB1 CHANGE c1 1 1000")
	e.ReceivedAt = at.Add(3 * time.Second)
	must(t, svc.Handle(e))
	sessions, err := s.List("", false, 0)
	must(t, err)
	if len(sessions) != 1 {
		t.Fatal("manual suppression failed")
	}
	must(t, s.Close())
	s, err = OpenStore(dir)
	must(t, err)
	defer s.Close()
	svc = NewService(c, s)
	must(t, svc.Handle(e))
	sessions, err = s.List("", false, 0)
	must(t, err)
	if len(sessions) != 2 {
		t.Fatal("restart did not reenable recovery")
	}
	recovered := sessions[0]
	must(t, s.Close())
	s, err = OpenStore(dir)
	must(t, err)
	defer s.Close()
	if get(t, s, recovered.ID).State != "active" {
		t.Fatal("active session lost")
	}
}
func TestRevisionAndIndependentPrinters(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC()
	a := apply(t, s, "M118 FB1 START c1 One", at)
	b := apply(t, s, "M118 FB1 START c2 Two", at)
	ss := get(t, s, a)
	must(t, s.Plan(a, ss.Revision, "add", ""))
	if err := s.Plan(a, ss.Revision, "add", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit accepted")
	}
	apply(t, s, "M118 FB1 STOP c2 1 999", at.Add(time.Second))
	if get(t, s, a).State != "active" || get(t, s, b).TotalMG() != 999 {
		t.Fatal("printer isolation failed")
	}
}
func TestPendingCoalesces(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC()
	id := apply(t, s, "M118 FB1 START c1 Print", at)
	apply(t, s, "M118 FB1 STOP c1 1 10000", at.Add(time.Second))
	ss := get(t, s, id)
	sid := ss.Sections[0].ID
	must(t, s.SetSection(id, sid, -1, 1, nil, false))
	must(t, s.SetSection(id, sid, -1, 2, nil, false))
	must(t, s.SetSection(id, sid, -1, 0, nil, false))
	if w, err := s.Claim(time.Now()); err != nil || w != nil {
		t.Fatalf("unassigned section still charged: %+v %v", w, err)
	}
	must(t, s.SetSection(id, sid, -1, 2, nil, false))
	w, err := s.Claim(time.Now())
	must(t, err)
	if w == nil || w.Operation.DeltaMG != 10000 {
		t.Fatal(w)
	}
	must(t, s.SetSection(id, sid, -1, 3, nil, false))
	must(t, s.Finish(*w, "applied", "", nil))
	ss = get(t, s, id)
	if ss.Sections[0].Applied[2] != 10000 {
		t.Fatal("lost in-flight charge")
	}
	w, err = s.Claim(time.Now())
	must(t, err)
	if w.Operation.SpoolID != 2 || w.Operation.DeltaMG != -10000 {
		t.Fatal("refund should precede new charge")
	}
	must(t, s.Finish(*w, "conflict", "test", nil))
	if w, err = s.Claim(time.Now()); err != nil || w != nil {
		t.Fatal("replacement charge bypassed failed refund")
	}
}
func TestFixedPrecision(t *testing.T) {
	for _, v := range []string{"0", "0.001", "12.34", "999999.999"} {
		mg, err := parseMG(v)
		must(t, err)
		if mg == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"NaN", "-1", "1.0001", "1e3", "1000000"} {
		if _, err := parseMG(v); err == nil {
			t.Fatal(fmt.Sprintf("accepted %s", v))
		}
	}
}

func TestLifecycleBurstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	must(t, err)
	at := time.Now().UTC()
	id := apply(t, s, "M118 FB1 START c1 Retry print", at)
	if got := apply(t, s, "M118 FB1 START c1 Retry print", at.Add(1100*time.Millisecond)); got != "duplicate" {
		t.Fatal(got)
	}
	must(t, s.Close())
	s, err = OpenStore(dir)
	must(t, err)
	defer s.Close()
	if got := apply(t, s, "M118 FB1 START c1 Retry print", at.Add(2200*time.Millisecond)); got != "duplicate" {
		t.Fatal(got)
	}
	ss := get(t, s, id)
	if ss.State != "active" {
		t.Fatal("retry superseded session")
	}
	apply(t, s, "M118 FB1 STOP c1 1 1234", at.Add(time.Minute))
	if got := apply(t, s, "M118 FB1 STOP c1 1 1234", at.Add(time.Minute+2200*time.Millisecond)); got != "duplicate" {
		t.Fatal(got)
	}
	if got := apply(t, s, "M118 FB1 START c1 Retry print", at.Add(2*time.Minute)); got == id || got == "duplicate" {
		t.Fatal("new run suppressed")
	}
}

func TestChangeBurstSurvivesLossAndRestart(t *testing.T) {
	for delivered := 1; delivered < 8; delivered++ {
		t.Run(fmt.Sprintf("copies_%03b", delivered), func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenStore(dir)
			must(t, err)
			defer func() { s.Close() }()
			at := time.Now().UTC()
			id := apply(t, s, "M118 FB1 START c1 Sock", at)
			must(t, s.Plan(id, -1, "add", ""))
			ss := get(t, s, id)
			first, second := ss.Sections[0].ID, ss.Sections[1].ID
			must(t, s.SetSection(id, first, -1, 7, nil, false))
			must(t, s.SetSection(id, second, -1, 5, nil, false))
			for copy := 0; copy < 3; copy++ {
				if delivered&(1<<copy) == 0 {
					continue
				}
				apply(t, s, "M118 FB1 CHANGE c1 1 1445", at.Add(time.Minute+time.Duration(copy)*1100*time.Millisecond))
				must(t, s.Close())
				s, err = OpenStore(dir)
				must(t, err)
			}
			ss = get(t, s, id)
			if ss.Current != 2 || len(ss.Sections) != 2 || ss.TotalMG() != 1445 || len(ss.Events) != 2 {
				t.Fatalf("lost or repeated boundary: %+v", ss)
			}
			if ss.Sections[0].ID != first || ss.Sections[1].ID != second || ss.Sections[1].State != "current" || ss.Sections[1].SpoolID != 5 {
				t.Fatal("retry lost the planned section or its spool")
			}
			ops := ss.Sections[0].Operations
			if len(ops) != 1 || ops[0].SpoolID != 7 || ops[0].DeltaMG != 1445 || len(ss.Sections[1].Operations) != 0 {
				t.Fatalf("boundary charged incorrectly: %+v", ss.Sections)
			}
		})
	}
}
