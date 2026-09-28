package buddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExpectedSectionEditsSurviveUnrelatedChanges(t *testing.T) {
	s := testStore(t)
	id, sid := completed(t, s)
	original := get(t, s, id)
	must(t, s.Edit(id, -1, func(ss *Session) error { ss.audit("sync", "background progress"); return nil }))
	if err := s.SetSection(id, sid, original.Revision, 9, nil, false); !errors.Is(err, ErrConflict) {
		t.Fatal("legacy revision protection lost", err)
	}
	must(t, s.SetSectionExpected(id, sid, 9, nil, false, SectionExpectation{}))
	if err := s.SetSectionExpected(id, sid, 12, nil, false, SectionExpectation{}); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent assignment overwritten", err)
	}
	must(t, s.SetSectionExpected(id, sid, 12, nil, false, SectionExpectation{Spool: 9}))
	weight := int64(12500)
	must(t, s.SetSectionExpected(id, sid, 0, &weight, true, SectionExpectation{}))
	if err := s.SetSectionExpected(id, sid, 0, nil, true, SectionExpectation{}); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent weight overwritten", err)
	}
	must(t, s.SetSectionExpected(id, sid, 0, nil, true, SectionExpectation{Override: &weight}))
	if get(t, s, id).Sections[0].OverrideMG != nil {
		t.Fatal("reported weight not restored")
	}
}

func TestLiveViewsAndEnhancedForms(t *testing.T) {
	s := testStore(t)
	id, sid := completed(t, s)
	must(t, s.CacheSpools([]Spool{{ID: 7, Label: "#7 · Purple PLA"}, {ID: 12, Label: "#12 · White PLA"}}))
	service := NewService(testConfig(), s)
	h, err := NewWeb(service)
	must(t, err)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		return rw
	}
	response := request("GET", "/api/live?view=session&id="+id, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var page livePage
	must(t, json.Unmarshal(response.Body.Bytes(), &page))
	if page.Catalog == nil || page.CSRF == "" {
		t.Fatal("catalog or credentials missing")
	}
	known := map[string]string{}
	for _, r := range page.Regions {
		known[r.ID] = r.Version
		if r.ID == "provenance" {
			t.Fatal("closed audit transferred")
		}
		if r.ID == "sections" && strings.Contains(r.HTML, `<option value="7"`) {
			t.Fatal("entire catalog repeated inside form updates")
		}
	}
	b, _ := json.Marshal(known)
	response = request("GET", "/api/live?view=session&id="+id+"&known="+url.QueryEscape(string(b)), "")
	var unchanged livePage
	must(t, json.Unmarshal(response.Body.Bytes(), &unchanged))
	if len(unchanged.Regions) != 0 {
		t.Fatal("unchanged regions transferred", unchanged.Regions)
	}
	data := url.Values{"csrf": {page.CSRF}, "revision": {strconvI(page.Revision)}, "section": {sid}, "spool": {"7"}, "expected_spool": {"0"}}
	ch, stop := s.changes.subscribe()
	defer stop()
	must(t, s.Edit(id, -1, func(ss *Session) error { ss.audit("sync", "background progress"); return nil }))
	<-ch
	response = request("POST", "/sessions/"+id+"/spool", data.Encode())
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"location"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("save did not notify")
	}
	data.Set("spool", "12")
	response = request("POST", "/sessions/"+id+"/spool", data.Encode())
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	select {
	case <-ch:
		t.Fatal("failed edit published a change")
	default:
	}
	if get(t, s, id).Sections[0].SpoolID != 7 {
		t.Fatal("conflict overwrote assignment")
	}
	service.Spoolman.status(errors.New("test outage"))
	select {
	case <-ch:
	default:
		t.Fatal("catalog outage did not notify")
	}
	response = request("GET", "/api/live?view=dashboard", "")
	if !strings.Contains(response.Body.String(), "test outage") {
		t.Fatal("catalog outage missing from dashboard")
	}
}

func TestLiveEndpointsRequireAuthentication(t *testing.T) {
	s := testStore(t)
	c := testConfig()
	c.AuthUser = "buddy"
	c.AuthPassword = "secret"
	h, err := NewWeb(NewService(c, s))
	must(t, err)
	for _, path := range []string{"/api/events", "/api/live?view=dashboard"} {
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, httptest.NewRequest("GET", path, nil))
		if rw.Code != 401 {
			t.Fatal(path, rw.Code)
		}
	}
}

func TestCatalogRefreshPublishesProgressAndCompletion(t *testing.T) {
	s := testStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		rw.Write([]byte(`[{"id":7,"used_weight":10,"filament":{"name":"Purple","material":"PLA"}}]`))
	}))
	defer server.Close()
	c := testConfig()
	c.Spoolman.URL = server.URL
	client := NewSpoolman(c, s)
	ch, stop := s.changes.subscribe()
	defer stop()
	done := make(chan error, 1)
	go func() { done <- client.Refresh(context.Background()) }()
	<-entered
	refreshing := client.Refreshing()
	notified := len(ch) > 0
	close(release)
	must(t, <-done)
	if !refreshing || !notified || client.Refreshing() || client.RefreshedAt().IsZero() {
		t.Fatal("refresh lifecycle was not published")
	}
	spools, err := s.Spools()
	must(t, err)
	if len(spools) != 1 || spools[0].ID != 7 {
		t.Fatal("notification completed before catalog commit", spools)
	}
}

func TestExpectedEditsPreserveInventoryAfterWorkerProgress(t *testing.T) {
	s := testStore(t)
	c, inventory := fakeClient(t, s)
	id, sid := completed(t, s)
	must(t, s.SetSectionExpected(id, sid, 1, nil, false, SectionExpectation{}))
	drain(t, c)               // Changes session revision without changing the assigned spool.
	inventory.used[1] += 5000 // Independent external consumption must survive.
	must(t, s.SetSectionExpected(id, sid, 2, nil, false, SectionExpectation{Spool: 1}))
	drain(t, c)
	if inventory.used[1] != 105000 || inventory.used[2] != 212000 {
		t.Fatal(inventory.used)
	}
	weight := int64(15000)
	must(t, s.SetSectionExpected(id, sid, 0, &weight, true, SectionExpectation{}))
	drain(t, c)
	if inventory.used[1] != 105000 || inventory.used[2] != 215000 {
		t.Fatal(inventory.used)
	}
}
