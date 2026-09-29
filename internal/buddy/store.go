package buddy

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict; reload the page and check the section status")
var ErrUnassignedSpools = errors.New("Assign a spool to every section before archiving this session.")

type Store struct {
	changes changes
	db      *sql.DB
	lock    *os.File
	mu      sync.Mutex
}

func newID() string { return rand.Text() }
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".filament-buddy.lock"), os.O_CREATE|os.O_RDWR, 0640)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("data directory already in use")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "filament-buddy.db"))
	if err != nil {
		f.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: f}
	if err = s.initialize(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { err := s.db.Close(); s.lock.Close(); return err }
func (s *Store) initialize() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;`); err != nil {
		return err
	}
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > 1 {
		return errors.New("database schema is newer than this application")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,printer TEXT NOT NULL,state TEXT NOT NULL,archived INTEGER NOT NULL,opened TEXT NOT NULL,work INTEGER NOT NULL,body BLOB NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS active_printer ON sessions(printer) WHERE state='active';
 CREATE INDEX IF NOT EXISTS history ON sessions(archived,opened DESC);
 CREATE INDEX IF NOT EXISTS work ON sessions(work);
 CREATE TABLE IF NOT EXISTS recent(printer TEXT NOT NULL,raw TEXT NOT NULL,at TEXT NOT NULL,PRIMARY KEY(printer,raw));
 CREATE TABLE IF NOT EXISTS spools(id INTEGER PRIMARY KEY,body BLOB NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		return err
	}
	// A crash after sending but before recording success must never cause replay.
	rows, err := s.db.Query(`SELECT body FROM sessions WHERE work=1`)
	if err != nil {
		return err
	}
	var sessions []Session
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		var ss Session
		if err = json.Unmarshal(b, &ss); err != nil {
			rows.Close()
			return err
		}
		sessions = append(sessions, ss)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ss := range sessions {
		for i := range ss.Sections {
			for j := range ss.Sections[i].Operations {
				op := &ss.Sections[i].Operations[j]
				if op.State == "inflight" {
					op.State = "uncertain"
					op.Error = "Service stopped during request. Verify Spoolman before resolving."
					ss.audit("interrupted request", op.ID)
				}
			}
		}
		if err = s.save(s.db, &ss); err != nil {
			return err
		}
	}
	return nil
}

type querier interface{ QueryRow(string, ...any) *sql.Row }
type executor interface {
	Exec(string, ...any) (sql.Result, error)
}

func load(q querier, id string) (Session, error) {
	var b []byte
	err := q.QueryRow(`SELECT body FROM sessions WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	var ss Session
	if err == nil {
		err = json.Unmarshal(b, &ss)
	}
	return ss, err
}
func (s *Store) save(q executor, ss *Session) error {
	ss.Revision++
	work := false
	for _, v := range ss.Sections {
		for _, op := range v.Operations {
			if op.State == "pending" || op.State == "inflight" || op.State == "uncertain" || op.State == "conflict" {
				work = true
			}
		}
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return err
	}
	_, err = q.Exec(`INSERT INTO sessions VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,archived=excluded.archived,work=excluded.work,body=excluded.body`, ss.ID, ss.PrinterID, ss.State, ss.Archived, ss.OpenedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), work, b)
	return err
}
func (s *Store) Get(id string) (Session, error) { return load(s.db, id) }
func (s *Store) List(printer string, archived bool, offset int) ([]Session, error) {
	return s.list(`SELECT body FROM sessions WHERE archived=? AND (?='' OR printer=?) ORDER BY opened DESC LIMIT 51 OFFSET ?`, archived, printer, printer, offset)
}
func (s *Store) list(query string, args ...any) ([]Session, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var ss Session
		if err = json.Unmarshal(b, &ss); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}
func (s *Store) Edit(id string, revision int, fn func(*Session) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ss, err := load(tx, id)
	if err != nil {
		return err
	}
	if revision >= 0 && ss.Revision != revision {
		return ErrConflict
	}
	if err = fn(&ss); err != nil {
		return err
	}
	reconcile(&ss)
	if err = s.save(tx, &ss); err != nil {
		return err
	}
	return s.changed(tx.Commit())
}
func newSection(n int, state string) Section {
	return Section{ID: newID(), Number: n, State: state, Applied: map[int]int64{}}
}
func ensureSection(ss *Session, n int) {
	for len(ss.Sections) < n {
		ss.Sections = append(ss.Sections, newSection(len(ss.Sections)+1, "missing"))
	}
}
func closeSession(ss *Session, reason string, at time.Time) {
	ss.State = "closed"
	ss.CloseReason = reason
	ss.ClosedAt = &at
	for i := range ss.Sections {
		v := &ss.Sections[i]
		if v.State == "current" {
			v.State = "incomplete"
		}
		if v.State == "planned" {
			v.State = "unreached"
		}
	}
	ss.audit("closed", reason)
}
func (s *Store) Apply(e Event) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var recent string
	err = tx.QueryRow(`SELECT at FROM recent WHERE printer=? AND raw=?`, e.PrinterID, e.Raw).Scan(&recent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	at, _ := time.Parse(time.RFC3339Nano, recent)
	window := 2 * time.Second
	if e.Kind == "START" || e.Kind == "STOP" {
		window = 5 * time.Second
	}
	if !at.IsZero() && e.ReceivedAt.Sub(at) < window {
		return "duplicate", nil
	}
	if _, err = tx.Exec(`DELETE FROM recent WHERE at<?`, e.ReceivedAt.Add(-5*time.Second).Format("2006-01-02T15:04:05.000000000Z07:00")); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT OR REPLACE INTO recent VALUES(?,?,?)`, e.PrinterID, e.Raw, e.ReceivedAt.Format("2006-01-02T15:04:05.000000000Z07:00")); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(`SELECT id FROM sessions WHERE printer=? AND state='active'`, e.PrinterID).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if id == "" && e.Kind == "STOP" {
		return "orphan end", s.changed(tx.Commit())
	}
	var ss Session
	if id != "" {
		ss, err = load(tx, id)
		if err != nil {
			return "", err
		}
	}
	if e.Kind == "START" && id != "" {
		closeSession(&ss, "superseded", e.ReceivedAt)
		reconcile(&ss)
		if err = s.save(tx, &ss); err != nil {
			return "", err
		}
		id = ""
	}
	if id == "" {
		ss = Session{ID: newID(), PrinterID: e.PrinterID, Name: e.Name, State: "active", OpenedAt: e.ReceivedAt, Recovered: e.Kind == "CHANGE", Current: 1}
		if ss.Recovered {
			ss.Name = "Recovered print"
		}
		ss.Sections = []Section{newSection(1, "current")}
		ss.audit("opened", e.Kind)
	}
	ss.Events = append(ss.Events, e)
	if e.Kind != "START" {
		ensureSection(&ss, e.Section)
		v := &ss.Sections[e.Section-1]
		if v.ReportedMG != nil {
			if *v.ReportedMG != e.Milligrams {
				ss.audit("conflicting marker", e.Raw)
				if err = s.save(tx, &ss); err != nil {
					return "", err
				}
				return "conflicting marker", s.changed(tx.Commit())
			}
			return "duplicate", s.changed(tx.Commit())
		}
		if e.Kind == "STOP" && e.Section < ss.Current {
			ss.audit("out-of-order end", e.Raw)
			if err = s.save(tx, &ss); err != nil {
				return "", err
			}
			return "out-of-order end", s.changed(tx.Commit())
		}
		v.ReportedMG = &e.Milligrams
		v.State = "complete"
		if e.Kind == "CHANGE" && e.Section >= ss.Current {
			for i := range ss.Sections {
				if ss.Sections[i].Number < e.Section && ss.Sections[i].ReportedMG == nil {
					ss.Sections[i].State = "missing"
				}
			}
			ss.Current = e.Section + 1
			ensureSection(&ss, ss.Current)
			ss.Sections[ss.Current-1].State = "current"
		}
		if e.Kind == "STOP" {
			for i := range ss.Sections {
				if ss.Sections[i].Number < e.Section && ss.Sections[i].ReportedMG == nil {
					ss.Sections[i].State = "missing"
				}
			}
			closeSession(&ss, "end", e.ReceivedAt)
		}
	}
	reconcile(&ss)
	if err = s.save(tx, &ss); err != nil {
		return "", err
	}
	return ss.ID, s.changed(tx.Commit())
}

// Pending requests have not reached Spoolman and may be replaced. In-flight
// or ambiguous requests freeze this section until their outcome is resolved.
func reconcile(ss *Session) {
	for i := range ss.Sections {
		v := &ss.Sections[i]
		blocked := false
		for _, op := range v.Operations {
			if op.State == "inflight" || op.State == "uncertain" || op.State == "conflict" {
				blocked = true
			}
		}
		if blocked {
			continue
		}
		desired := map[int]int64{}
		if w := v.Weight(); w != nil && v.SpoolID != 0 {
			desired[v.SpoolID] = *w
		}
		delta := map[int]int64{}
		for id, w := range desired {
			delta[id] += w
		}
		for id, w := range v.Applied {
			delta[id] -= w
		}
		same := true
		pending := map[int]int64{}
		for _, op := range v.Operations {
			if op.State == "pending" {
				pending[op.SpoolID] += op.DeltaMG
			}
		}
		for id, w := range delta {
			if pending[id] != w {
				same = false
			}
		}
		for id, w := range pending {
			if delta[id] != w {
				same = false
			}
		}
		if same {
			continue
		}
		now := time.Now().UTC()
		for j := range v.Operations {
			if v.Operations[j].State == "pending" {
				v.Operations[j].State = "superseded"
				v.Operations[j].UpdatedAt = now
			}
		}
		var ids []int
		for id, w := range delta {
			if w != 0 {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(a, b int) bool {
			if (delta[ids[a]] < 0) != (delta[ids[b]] < 0) {
				return delta[ids[a]] < 0
			}
			return ids[a] < ids[b]
		})
		for _, id := range ids {
			v.Operations = append(v.Operations, Operation{ID: newID(), SpoolID: id, DeltaMG: delta[id], State: "pending", CreatedAt: now, UpdatedAt: now})
		}
	}
}
func section(ss *Session, id string) (*Section, error) {
	for i := range ss.Sections {
		if ss.Sections[i].ID == id {
			return &ss.Sections[i], nil
		}
	}
	return nil, ErrNotFound
}

// SectionExpectation compares the field the user actually edited, rather than
// rejecting drafts because a worker advanced the whole-session revision.
type SectionExpectation struct {
	Spool    int
	Override *int64
}

func (s *Store) SetSection(id, sid string, rev, spool int, override *int64, editWeight bool) error {
	return s.setSection(id, sid, rev, spool, override, editWeight, nil)
}
func (s *Store) SetSectionExpected(id, sid string, spool int, override *int64, editWeight bool, expected SectionExpectation) error {
	return s.setSection(id, sid, -1, spool, override, editWeight, &expected)
}
func (s *Store) setSection(id, sid string, rev, spool int, override *int64, editWeight bool, expected *SectionExpectation) error {
	return s.Edit(id, rev, func(ss *Session) error {
		v, err := section(ss, sid)
		if err != nil {
			return err
		}
		if expected != nil {
			matches := v.SpoolID == expected.Spool
			if editWeight {
				matches = (v.OverrideMG == nil && expected.Override == nil) || (v.OverrideMG != nil && expected.Override != nil && *v.OverrideMG == *expected.Override)
			}
			if !matches {
				return fmt.Errorf("%w: this value changed while you were editing", ErrConflict)
			}
		}
		if spool < 0 {
			return ErrConflict
		}
		if editWeight {
			if v.State == "planned" || v.State == "unreached" {
				return ErrConflict
			}
			if override != nil && (*override < 0 || *override > MaxMilligrams) {
				return ErrConflict
			}
			v.OverrideMG = override
			ss.audit("weight override", fmt.Sprintf("Section %d: %v", v.Number, weightText(override)))
		} else {
			v.SpoolID = spool
			ss.audit("spool assignment", fmt.Sprintf("Section %d → spool %d", v.Number, spool))
			if spool == 0 && ss.Archived {
				ss.Archived = false
				ss.audit("archive", fmt.Sprintf("false: automatically restored because Section %d has no assigned spool", v.Number))
			}
		}
		return nil
	})
}
func weightText(w *int64) string {
	if w == nil {
		return "reported value"
	}
	return fmt.Sprintf("%.3f g", float64(*w)/1000)
}
func (s *Store) Plan(id string, rev int, action, sid string) error {
	return s.Edit(id, rev, func(ss *Session) error {
		if ss.State != "active" {
			return ErrConflict
		}
		if action == "add" {
			if len(ss.Sections) >= MaxSection {
				return ErrConflict
			}
			ss.Sections = append(ss.Sections, newSection(len(ss.Sections)+1, "planned"))
			ss.audit("planned section", "added")
			return nil
		}
		v, err := section(ss, sid)
		if err != nil {
			return err
		}
		if v.State != "planned" {
			return ErrConflict
		}
		i := v.Number - 1
		switch action {
		case "remove":
			ss.Sections = append(ss.Sections[:i], ss.Sections[i+1:]...)
		case "up", "down":
			j := i - 1
			if action == "down" {
				j = i + 1
			}
			if j < 0 || j >= len(ss.Sections) || ss.Sections[j].State != "planned" {
				return ErrConflict
			}
			ss.Sections[i], ss.Sections[j] = ss.Sections[j], ss.Sections[i]
		default:
			return ErrConflict
		}
		for i := range ss.Sections {
			ss.Sections[i].Number = i + 1
		}
		ss.audit("planned section", action)
		return nil
	})
}
func (s *Store) Archive(id string, rev int, archive bool) error {
	return s.Edit(id, rev, func(ss *Session) error {
		if ss.State != "closed" {
			return ErrConflict
		}
		if archive && ss.UnassignedSections() > 0 {
			return fmt.Errorf("%w: %w", ErrConflict, ErrUnassignedSpools)
		}
		ss.Archived = archive
		ss.audit("archive", fmt.Sprint(archive))
		return nil
	})
}
func (s *Store) CloseSession(id string, rev int) error {
	return s.Edit(id, rev, func(ss *Session) error {
		if ss.State != "active" {
			return ErrConflict
		}
		closeSession(ss, "manual", time.Now().UTC())
		return nil
	})
}
func (s *Store) CacheSpools(spools []Spool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range spools {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`INSERT OR REPLACE INTO spools VALUES(?,?)`, v.ID, b); err != nil {
			return err
		}
	}
	return s.changed(tx.Commit())
}
func (s *Store) Spools() ([]Spool, error) {
	rows, err := s.db.Query(`SELECT body FROM spools ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Spool{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v Spool
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
