package buddy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDisplayNameFormsAndLiveViews(t *testing.T) {
	store := testStore(t)
	id := apply(t, store, "M118 FB1 START c1 Print", time.Now())
	s := NewService(testConfig(), store)
	s.Config.AuthUser, s.Config.AuthPassword = "buddy", "secret"
	h, err := NewWeb(s)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, data url.Values, auth, jsonResponse bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(data.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if jsonResponse {
			r.Header.Set("Accept", "application/json")
		}
		if auth {
			r.SetBasicAuth("buddy", "secret")
		}
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		return rw
	}
	live := request("GET", "/api/live?view=session&id="+id, nil, true, true)
	var page livePage
	if err := json.Unmarshal(live.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	name := "Vase <blue> & café 🌿"
	data := url.Values{"csrf": {page.CSRF}, "display_name": {"  " + name + "  "}, "expected_display_name": {""}}
	path := "/sessions/" + id + "/name"
	if rw := request("POST", path, data, false, true); rw.Code != 401 {
		t.Fatal(rw.Code)
	}
	data.Set("csrf", "wrong")
	if rw := request("POST", path, data, true, true); rw.Code != 403 {
		t.Fatal(rw.Code)
	}
	data.Set("csrf", page.CSRF)
	if rw := request("POST", path, data, true, true); rw.Code != 200 {
		t.Fatal(rw.Code, rw.Body.String())
	}
	for _, path := range []string{"/", "/sessions/" + id} {
		rw := request("GET", path, nil, true, false)
		if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Vase &lt;blue&gt; &amp; café 🌿") || strings.Contains(rw.Body.String(), name) {
			t.Fatal(path, rw.Code, rw.Body.String())
		}
	}
	live = request("GET", "/api/live?view=session&id="+id, nil, true, true)
	if err := json.Unmarshal(live.Body.Bytes(), &page); err != nil || page.Title != name {
		t.Fatal(err, page.Title)
	}
	found := false
	for _, region := range page.Regions {
		if region.ID == "session-heading" && strings.Contains(region.HTML, "Vase &lt;blue&gt;") {
			found = true
		}
	}
	if !found {
		t.Fatal("renamed heading missing from live update")
	}
	if rw := request("POST", path, data, true, true); rw.Code != 409 || !strings.Contains(rw.Body.String(), "display name changed") {
		t.Fatal(rw.Code, rw.Body.String())
	}
	data.Set("expected_display_name", name)
	for _, invalid := range []string{strings.Repeat("a", 201), "two\nlines", "bad\x00name", "bad\u2028name", string([]byte{0xff})} {
		data.Set("display_name", invalid)
		if rw := request("POST", path, data, true, true); rw.Code != 400 {
			t.Fatalf("invalid name %q: %d %s", invalid, rw.Code, rw.Body.String())
		}
	}
	data.Set("display_name", " ")
	if rw := request("POST", path, data, true, false); rw.Code != 303 || rw.Header().Get("Location") != "/sessions/"+id {
		t.Fatal(rw.Code, rw.Body.String())
	}
	live = request("GET", "/api/live?view=session&id="+id, nil, true, true)
	if err := json.Unmarshal(live.Body.Bytes(), &page); err != nil || page.Title != "Print" {
		t.Fatal(err, page.Title)
	}
	data.Del("expected_display_name")
	if rw := request("POST", path, data, true, true); rw.Code != 400 {
		t.Fatal("missing expectation accepted", rw.Code)
	}
	data.Set("expected_display_name", "")
	if rw := request("POST", "/sessions/missing/name", data, true, true); rw.Code != 404 {
		t.Fatal(rw.Code)
	}
}

func TestDisplayNamePreservesAccountingAndSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	must(t, err)
	t.Cleanup(func() { s.Close() })
	at := time.Now()
	id := apply(t, s, "M118 FB1 START c1 Print", at)
	before := get(t, s, id)
	must(t, s.SetSpool(id, before.Sections[0].ID, -1, 7))
	apply(t, s, "M118 FB1 CHANGE c1 1 12000", at.Add(time.Second))
	current := get(t, s, id)
	ch, stop := s.changes.subscribe()
	defer stop()
	must(t, s.SetDisplayName(id, "Desk organizer", ""))
	select {
	case <-ch:
	default:
		t.Fatal("rename did not notify live views")
	}
	renamed := get(t, s, id)
	if renamed.Name != before.Name || !reflect.DeepEqual(renamed.Events, current.Events) || !reflect.DeepEqual(renamed.Sections, current.Sections) {
		t.Fatal("rename changed accounting or markers", renamed)
	}
	if renamed.History[len(renamed.History)-1].Action != "display name" {
		t.Fatal("missing name audit")
	}
	if err := s.SetDisplayName(id, "Lost edit", ""); !errors.Is(err, ErrDisplayNameConflict) {
		t.Fatal(err)
	}
	apply(t, s, "M118 FB1 START c1 Print", at.Add(2200*time.Millisecond))
	sessions, err := s.List("c1", false, 0)
	must(t, err)
	if len(sessions) != 1 || sessions[0].ID != id {
		t.Fatal("START retry split renamed session")
	}
	apply(t, s, "M118 FB1 STOP c1 2 5000", at.Add(3*time.Second))
	closed := get(t, s, id)
	if closed.Title() != "Desk organizer" || closed.State != "closed" || closed.TotalMG() != 17000 {
		t.Fatal(closed)
	}
	assignUnassignedSpools(t, s, id)
	must(t, s.Archive(id, -1, true))
	must(t, s.SetDisplayName(id, "Finished organizer", "Desk organizer"))
	must(t, s.Close())
	s, err = OpenStore(dir)
	must(t, err)
	closed = get(t, s, id)
	if closed.Title() != "Finished organizer" || closed.Name != "Print" || !closed.Archived {
		t.Fatal(closed)
	}
	must(t, s.SetDisplayName(id, "", "Finished organizer"))
	if get(t, s, id).Title() != "Print" {
		t.Fatal("original name lost")
	}
	nextID := apply(t, s, "M118 FB1 START c1 Print", at.Add(10*time.Second))
	next := get(t, s, nextID)
	if next.ID == id || next.DisplayName != "" || next.Title() != "Print" {
		t.Fatal("new print inherited an override", next)
	}
}

func TestDisplayNameLegacyDocumentAndRecoveredSession(t *testing.T) {
	s := testStore(t)
	id := apply(t, s, "M118 FB1 CHANGE c1 1 12000", time.Now())
	var body []byte
	must(t, s.db.QueryRow(`SELECT body FROM sessions WHERE id=?`, id).Scan(&body))
	if strings.Contains(string(body), "DisplayName") {
		t.Fatal("empty override should be omitted for legacy compatibility")
	}
	before := get(t, s, id)
	if before.Title() != "Recovered print" {
		t.Fatal(before)
	}
	must(t, s.SetDisplayName(id, "Recovered vase", ""))
	if ss := get(t, s, id); ss.Title() != "Recovered vase" || !ss.Recovered {
		t.Fatal(ss)
	}
	must(t, s.SetDisplayName(id, "", "Recovered vase"))
	if get(t, s, id).Title() != before.Name {
		t.Fatal("original recovered name lost")
	}
}
