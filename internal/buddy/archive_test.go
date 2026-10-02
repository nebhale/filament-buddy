package buddy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func assignUnassignedSpools(t *testing.T, s *Store, id string) {
	t.Helper()
	for _, section := range get(t, s, id).Sections {
		if section.SpoolID == 0 {
			must(t, s.SetSpool(id, section.ID, -1, 7))
		}
	}
}

func TestArchiveRequiresAssignedSpoolsInEverySection(t *testing.T) {
	for _, state := range []string{"current", "complete", "planned", "unreached", "missing", "incomplete"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			id, _ := completed(t, s)
			must(t, s.Edit(id, -1, func(ss *Session) error {
				ss.Sections[0].SpoolID = 7
				unassigned := newSection(2, state)
				unassigned.ReportedMG = ptr(0)
				ss.Sections = append(ss.Sections, unassigned)
				return nil
			}))
			before := get(t, s, id)
			err := s.Archive(id, before.Revision, true)
			if !errors.Is(err, ErrUnassignedSpools) || !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			if after := get(t, s, id); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected archive changed the session")
			}
			// Assignments remain valid when the catalog is unavailable or omits an ID.
			assignUnassignedSpools(t, s, id)
			must(t, s.Archive(id, get(t, s, id).Revision, true))
			if !get(t, s, id).Archived {
				t.Fatal("fully assigned session was not archived")
			}
		})
	}
}

func TestClearingSpoolRestoresArchivedSessionAtomically(t *testing.T) {
	for _, enhanced := range []bool{false, true} {
		t.Run(strconv.FormatBool(enhanced), func(t *testing.T) {
			dir := t.TempDir()
			s, openErr := OpenStore(dir)
			must(t, openErr)
			defer s.Close()
			id, sid := completed(t, s)
			assignUnassignedSpools(t, s, id)
			must(t, s.Archive(id, -1, true))
			original := get(t, s, id)
			// Both forms reject stale edits before changing the archive flag.
			var err error
			if enhanced {
				err = s.SetSpoolExpected(id, sid, 0, 8)
			} else {
				err = s.SetSpool(id, sid, original.Revision-1, 0)
			}
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(original, get(t, s, id)) {
				t.Fatal("stale edit changed the archived session", err)
			}
			must(t, s.SetSpool(id, sid, -1, 8))
			if !get(t, s, id).Archived {
				t.Fatal("assigned-spool edit restored the session")
			}
			before := get(t, s, id)
			updates, stop := s.changes.subscribe()
			defer stop()
			if enhanced {
				err = s.SetSpoolExpected(id, sid, 0, 8)
			} else {
				err = s.SetSpool(id, sid, before.Revision, 0)
			}
			must(t, err)
			after := get(t, s, id)
			if after.Archived || after.Sections[0].SpoolID != 0 || after.Revision != before.Revision+1 {
				t.Fatal("spool and archive were not saved together", after)
			}
			if len(after.History) != len(before.History)+2 || after.History[len(after.History)-1].Action != "archive" || !strings.Contains(after.History[len(after.History)-1].Detail, "automatically restored") {
				t.Fatal("automatic restoration was not audited", after.History)
			}
			select {
			case <-updates:
			default:
				t.Fatal("restoration did not notify live views")
			}
			main, err := s.List("", false, 0)
			must(t, err)
			archived, err := s.List("", true, 0)
			must(t, err)
			if len(main) != 1 || main[0].ID != id || len(archived) != 0 {
				t.Fatal("library indexes were not updated")
			}
			must(t, s.Close())
			reopened, err := OpenStore(dir)
			must(t, err)
			defer reopened.Close()
			if got := get(t, reopened, id); got.Archived || got.Sections[0].SpoolID != 0 {
				t.Fatal("restoration did not survive restart")
			}
		})
	}
}

func TestArchiveHTTPRejectsUnassignedSessionsAndAllowsOthers(t *testing.T) {
	b := newBulkHarness(t)
	assigned, unassigned := b.session(true), b.session(true)
	ss := get(t, b.s.Store, unassigned)
	must(t, b.s.Store.SetSpool(unassigned, ss.Sections[0].ID, ss.Revision, 0))
	for _, enhanced := range []bool{false, true} {
		values := url.Values{"csrf": {b.token}, "revision": {strconv.Itoa(get(t, b.s.Store, unassigned).Revision)}}
		r := httptest.NewRequest("POST", "/sessions/"+unassigned+"/archive", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if enhanced {
			r.Header.Set("Accept", "application/json")
		}
		out := httptest.NewRecorder()
		b.h.ServeHTTP(out, r)
		if out.Code != 409 || !strings.Contains(out.Body.String(), ErrUnassignedSpools.Error()) {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	reply := b.result("archive", b.form(assigned, unassigned))
	if bulkStatuses(reply)[assigned] != "success" || bulkStatuses(reply)[unassigned] != "conflict" || reply.Results[1].Error != ErrUnassignedSpools.Error() {
		t.Fatal(reply)
	}
	// Legacy archived sessions can still be restored even with missing assignments.
	must(t, b.s.Store.Edit(unassigned, -1, func(ss *Session) error { ss.Archived = true; return nil }))
	out := httptest.NewRecorder()
	b.h.ServeHTTP(out, httptest.NewRequest("GET", "/api/live?view=dashboard&archived=true", nil))
	var live livePage
	must(t, json.Unmarshal(out.Body.Bytes(), &live))
	var html string
	for _, region := range live.Regions {
		if region.ID == "library-list" {
			html = region.HTML
		}
	}
	if !strings.Contains(html, `value="`+unassigned+`" data-session-select data-eligible="true"`) {
		t.Fatal("legacy archived session cannot be selected for restore", html)
	}
	if bulkStatuses(b.result("restore", b.form(unassigned)))[unassigned] != "success" {
		t.Fatal("legacy restoration blocked")
	}
}
