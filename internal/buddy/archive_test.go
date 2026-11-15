package buddy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assignUnassignedSpools(t *testing.T, s *Store, id string) {
	t.Helper()
	for _, section := range get(t, s, id).Sections {
		if section.SpoolID == 0 {
			must(t, s.SetSpool(id, section.ID, -1, 7))
		}
	}
}

func TestArchiveWithoutReportedWeights(t *testing.T) {
	for _, reason := range []string{"manual", "superseded"} {
		t.Run(reason, func(t *testing.T) {
			s := testStore(t)
			at := time.Now()
			id := apply(t, s, "M118 FB1 START c1 No weights", at)
			must(t, s.Plan(id, -1, "add", ""))
			active := get(t, s, id)
			if err := s.Archive(id, active.Revision, true); !errors.Is(err, ErrConflict) {
				t.Fatal("active session could be archived", err)
			}
			if reason == "manual" {
				must(t, s.CloseSession(id, active.Revision))
			} else {
				apply(t, s, "M118 FB1 START c1 Next print", at.Add(time.Second))
			}
			before := get(t, s, id)
			if before.CloseReason != reason || before.UnassignedSections() != 2 {
				t.Fatal("unexpected closed session", before)
			}
			if err := s.Archive(id, active.Revision, true); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, get(t, s, id)) {
				t.Fatal("stale archive changed the session", err)
			}
			must(t, s.Archive(id, before.Revision, true))
			after := get(t, s, id)
			if !after.Archived || !reflect.DeepEqual(before.Sections, after.Sections) || !reflect.DeepEqual(before.Events, after.Events) {
				t.Fatal("archive changed weights or assignments", after)
			}
			if len(after.History) != len(before.History)+1 || after.History[len(after.History)-1].Action != "archive" {
				t.Fatal("archive was not audited", after.History)
			}
			must(t, s.Archive(id, after.Revision, false))
			if get(t, s, id).Archived {
				t.Fatal("session was not restored")
			}
		})
	}
}

func TestArchiveRequiresSpoolsForReportedZeroWeight(t *testing.T) {
	s := testStore(t)
	at := time.Now()
	id := apply(t, s, "M118 FB1 START c1 Zero weight", at)
	apply(t, s, "M118 FB1 STOP c1 1 0", at.Add(time.Second))
	before := get(t, s, id)
	if err := s.Archive(id, before.Revision, true); !errors.Is(err, ErrUnassignedSpools) || !reflect.DeepEqual(before, get(t, s, id)) {
		t.Fatal("reported zero weight bypassed assignment requirements", err)
	}
}

func TestClearingSpoolKeepsSessionWithoutWeightsArchived(t *testing.T) {
	for _, enhanced := range []bool{false, true} {
		t.Run(strconv.FormatBool(enhanced), func(t *testing.T) {
			s := testStore(t)
			id := apply(t, s, "M118 FB1 START c1 No weights", time.Now())
			sid := get(t, s, id).Sections[0].ID
			must(t, s.SetSpool(id, sid, -1, 7))
			must(t, s.CloseSession(id, -1))
			must(t, s.Archive(id, -1, true))
			before := get(t, s, id)
			if enhanced {
				must(t, s.SetSpoolExpected(id, sid, 0, 7))
			} else {
				must(t, s.SetSpool(id, sid, before.Revision, 0))
			}
			after := get(t, s, id)
			if !after.Archived || after.Sections[0].SpoolID != 0 || len(after.History) != len(before.History)+1 {
				t.Fatal("clearing an unused spool restored the session", after)
			}
		})
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

func TestArchiveControlsFollowReportedWeights(t *testing.T) {
	for _, weight := range []*int64{nil, ptr(0), ptr(12000)} {
		name := "no weight"
		if weight != nil {
			name = strconv.FormatInt(*weight, 10)
		}
		t.Run(name, func(t *testing.T) {
			b := newBulkHarness(t)
			at := time.Now()
			id := apply(t, b.s.Store, "M118 FB1 START c1 Archive controls", at)
			if weight == nil {
				must(t, b.s.Store.CloseSession(id, -1))
			} else {
				apply(t, b.s.Store, "M118 FB1 STOP c1 1 "+strconv.FormatInt(*weight, 10), at.Add(time.Second))
			}
			eligible := weight == nil
			for _, path := range []string{"/", "/sessions/" + id, "/api/live?view=dashboard", "/api/live?view=session&id=" + id} {
				out := httptest.NewRecorder()
				b.h.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
				if out.Code != 200 {
					t.Fatal(path, out.Code, out.Body.String())
				}
				html := out.Body.String()
				if strings.HasPrefix(path, "/api/live") {
					var live livePage
					must(t, json.Unmarshal(out.Body.Bytes(), &live))
					html = ""
					for _, region := range live.Regions {
						html += region.HTML
					}
				}
				if path == "/" || strings.Contains(path, "view=dashboard") {
					if !strings.Contains(html, `value="`+id+`" data-session-select data-eligible="`+strconv.FormatBool(eligible)+`"`) {
						t.Fatal(path, "incorrect bulk archive eligibility", html)
					}
				} else {
					button := regexp.MustCompile(`<button[^>]*>Archive session</button>`).FindString(html)
					if button == "" || strings.Contains(button, "disabled") == eligible {
						t.Fatal(path, "incorrect archive button state", button)
					}
				}
			}
		})
	}
}

func TestArchiveHTTPAllowsSessionsWithoutReportedWeights(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		for _, enhanced := range []bool{false, true} {
			t.Run("bulk="+strconv.FormatBool(bulk)+"/enhanced="+strconv.FormatBool(enhanced), func(t *testing.T) {
				b := newBulkHarness(t)
				id := apply(t, b.s.Store, "M118 FB1 START c1 No weights", time.Now())
				must(t, b.s.Store.CloseSession(id, -1))
				before := get(t, b.s.Store, id)
				path := "/sessions/" + id + "/archive"
				values := url.Values{"csrf": {b.token}, "revision": {strconv.Itoa(before.Revision)}}
				wantStatus := 303
				if bulk {
					path, values, wantStatus = "/sessions/bulk/archive", b.form(id), 200
				}
				r := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if enhanced {
					r.Header.Set("Accept", "application/json")
					wantStatus = 200
				}
				out := httptest.NewRecorder()
				b.h.ServeHTTP(out, r)
				after := get(t, b.s.Store, id)
				if out.Code != wantStatus || !after.Archived || !reflect.DeepEqual(before.Sections, after.Sections) {
					t.Fatal(out.Code, out.Body.String(), after)
				}
			})
		}
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
