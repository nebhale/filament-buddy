package buddy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUnassignedSectionsIncludesEverySectionState(t *testing.T) {
	zero := int64(0)
	for _, state := range []string{"current", "complete", "planned", "unreached", "missing", "incomplete"} {
		t.Run(state, func(t *testing.T) {
			session := Session{Sections: []Section{
				{State: state},
				{State: state, ReportedMG: &zero, Operations: []Operation{{State: "pending"}}},
				{State: state, SpoolID: 7},
			}}
			if got := session.UnassignedSections(); got != 2 {
				t.Fatalf("got %d, want 2 regardless of weight or synchronization status", got)
			}
		})
	}
	if (Session{}).UnassignedSections() != 0 {
		t.Fatal("empty session has unassigned sections")
	}
}

func TestUnassignedSpoolBadgesFollowSavedAssignments(t *testing.T) {
	store := testStore(t)
	at := time.Now()
	id := apply(t, store, "M118 FB1 START c1 Assignments", at)
	must(t, store.Plan(id, -1, "add", ""))
	session := get(t, store, id)
	h, err := NewWeb(NewService(testConfig(), store))
	must(t, err)
	badge := regexp.MustCompile(`<span class="badge unassigned-spools"[^>]*>([^<]*)</span>`)
	check := func(archived bool, want int) {
		t.Helper()
		archive := strconv.FormatBool(archived)
		for _, path := range []string{"/?archived=" + archive, "/sessions/" + id, "/api/live?view=dashboard&archived=" + archive, "/api/live?view=session&id=" + id} {
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, httptest.NewRequest("GET", path, nil))
			if rw.Code != 200 {
				t.Fatalf("%s: %d %s", path, rw.Code, rw.Body.String())
			}
			body := rw.Body.String()
			if strings.HasPrefix(path, "/api/live") {
				var page livePage
				must(t, json.Unmarshal(rw.Body.Bytes(), &page))
				body = ""
				for _, region := range page.Regions {
					body += region.HTML
				}
			}
			matches := badge.FindAllStringSubmatch(body, -1)
			if want == 0 {
				if len(matches) != 0 {
					t.Fatalf("%s: fully assigned session still has a badge", path)
				}
			} else if len(matches) != 1 || matches[0][1] != strconv.Itoa(want)+" unassigned" {
				t.Fatalf("%s: badges %v, want %d unassigned", path, matches, want)
			}
		}
	}
	check(false, 2)
	// A saved spool remains assigned even if it is absent from the catalog.
	must(t, store.SetSpool(id, session.Sections[0].ID, -1, 7))
	check(false, 1)
	apply(t, store, "M118 FB1 STOP c1 1 12000", at.Add(time.Second))
	check(false, 1)
	if err := store.Archive(id, -1, true); !errors.Is(err, ErrUnassignedSpools) {
		t.Fatal(err)
	}
	must(t, store.SetSpool(id, session.Sections[1].ID, -1, 7))
	check(false, 0)
	must(t, store.Archive(id, -1, true))
	check(true, 0)
	must(t, store.SetSpool(id, session.Sections[0].ID, -1, 0))
	check(false, 1)
}
