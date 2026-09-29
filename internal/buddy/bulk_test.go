package buddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

type bulkHarness struct {
	t     *testing.T
	s     *Service
	h     http.Handler
	token string
	seq   int
}

func newBulkHarness(t *testing.T) *bulkHarness {
	t.Helper()
	s := NewService(testConfig(), testStore(t))
	h, err := NewWeb(s)
	if err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest("GET", "/api/live?view=dashboard", nil))
	var page livePage
	if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil {
		t.Fatal(err, out.Body.String())
	}
	return &bulkHarness{t: t, s: s, h: h, token: page.CSRF}
}
func (b *bulkHarness) session(closed bool) string {
	b.t.Helper()
	b.seq++
	at := time.Now().Add(time.Duration(b.seq) * 2 * time.Hour)
	id := apply(b.t, b.s.Store, fmt.Sprintf("M118 FB1 START c1 Bulk %d", b.seq), at)
	if closed {
		apply(b.t, b.s.Store, "M118 FB1 STOP c1 1 12000", at.Add(time.Minute))
	}
	return id
}
func (b *bulkHarness) form(ids ...string) url.Values {
	values := url.Values{"csrf": {b.token}, "session": ids, "page": {"0"}, "confirmed": {"true"}}
	for _, id := range ids {
		revision := 0
		if ss, err := b.s.Store.Get(id); err == nil {
			revision = ss.Revision
		}
		values.Set("revision_"+id, strconv.Itoa(revision))
	}
	return values
}
func (b *bulkHarness) post(action string, values url.Values, enhanced bool) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest("POST", "/sessions/bulk/"+action, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if enhanced {
		r.Header.Set("Accept", "application/json")
	}
	out := httptest.NewRecorder()
	b.h.ServeHTTP(out, r)
	return out
}
func (b *bulkHarness) result(action string, values url.Values) bulkReply {
	b.t.Helper()
	out := b.post(action, values, true)
	if out.Code != 200 {
		b.t.Fatal(out.Code, out.Body.String())
	}
	var result bulkReply
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		b.t.Fatal(err, out.Body.String())
	}
	return result
}
func bulkStatuses(reply bulkReply) map[string]string {
	statuses := map[string]string{}
	for _, result := range reply.Results {
		statuses[result.ID] = result.Status
	}
	return statuses
}

func TestBulkRejectsInvalidSelectionBeforeAnyMutation(t *testing.T) {
	for _, kind := range []string{"empty", "duplicate", "malformed", "oversize", "missing revision", "negative revision", "duplicate revision", "invalid revision"} {
		t.Run(kind, func(t *testing.T) {
			b := newBulkHarness(t)
			id := b.session(true)
			values := b.form(id)
			switch kind {
			case "empty":
				values.Del("session")
			case "duplicate":
				values.Add("session", id)
			case "malformed":
				values.Add("session", "../outside")
			case "oversize":
				for i := 0; i < 50; i++ {
					values.Add("session", fmt.Sprintf("missing-%d", i))
				}
			case "missing revision":
				values.Del("revision_" + id)
			case "negative revision":
				values.Set("revision_"+id, "-1")
			case "duplicate revision":
				values.Add("revision_"+id, "0")
			case "invalid revision":
				values.Set("revision_"+id, "bad")
			}
			out := b.post("archive", values, true)
			if out.Code != 400 {
				t.Fatal(out.Code, out.Body.String())
			}
			ss, err := b.s.Store.Get(id)
			if err != nil || ss.Archived {
				t.Fatal("valid item was changed before request validation", ss, err)
			}
		})
	}
}

func TestBulkAuthenticationAndCSRF(t *testing.T) {
	b := newBulkHarness(t)
	id := b.session(true)
	form := b.form(id)
	form.Set("csrf", "invalid")
	if out := b.post("archive", form, true); out.Code != 403 {
		t.Fatal(out.Code)
	}
	b.s.Config.AuthUser, b.s.Config.AuthPassword = "buddy", "secret"
	form.Set("csrf", b.token)
	if out := b.post("archive", form, true); out.Code != 401 {
		t.Fatal(out.Code)
	}
	r := httptest.NewRequest("POST", "/sessions/bulk/archive", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	r.SetBasicAuth("buddy", "secret")
	out := httptest.NewRecorder()
	b.h.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestBulkEmptyLastPageReturnsToPreviousPage(t *testing.T) {
	b := newBulkHarness(t)
	first := b.session(true)
	for i := 0; i < 50; i++ {
		b.session(true)
	}
	form := b.form(first)
	form.Set("page", "1")
	form.Set("printer", "c1")
	reply := b.result("archive", form)
	location, err := url.Parse(reply.Location)
	if err != nil || location.Query().Get("page") != "0" || location.Query().Get("printer") != "c1" {
		t.Fatal(reply.Location, err)
	}
}

func TestBulkArchiveAndRestoreKeepAccountingAndRejectStaleSessions(t *testing.T) {
	b := newBulkHarness(t)
	first, stale, activeID := b.session(true), b.session(true), b.session(false)
	firstBefore, _ := b.s.Store.Get(first)
	form := b.form(first, stale, activeID, "missing")
	if err := b.s.Store.Edit(stale, -1, func(ss *Session) error { ss.audit("test", "concurrent change"); return nil }); err != nil {
		t.Fatal(err)
	}
	reply := b.result("archive", form)
	statuses := bulkStatuses(reply)
	if statuses[first] != "success" || statuses[stale] != "conflict" || statuses[activeID] != "conflict" || statuses["missing"] != "missing" {
		t.Fatal(reply)
	}
	archived, _ := b.s.Store.Get(first)
	if !archived.Archived || !reflect.DeepEqual(firstBefore.Sections, archived.Sections) {
		t.Fatal("archiving changed accounting", archived)
	}
	if len(archived.History) != len(firstBefore.History)+1 {
		t.Fatal("archive audit missing")
	}
	restored := b.result("restore", b.form(first))
	after, _ := b.s.Store.Get(first)
	if bulkStatuses(restored)[first] != "success" || after.Archived || !reflect.DeepEqual(firstBefore.Sections, after.Sections) {
		t.Fatal("restore changed accounting", restored, after)
	}
	if !strings.Contains(restored.Location, "archived=true") {
		t.Fatal("lost archived library", restored.Location)
	}
}

func TestBulkArchiveNativeResultsRetainFailedSelections(t *testing.T) {
	b := newBulkHarness(t)
	first, stale := b.session(true), b.session(true)
	form := b.form(first, stale)
	if err := b.s.Store.Edit(stale, -1, func(ss *Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	out := b.post("archive", form, false)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "1 session archived.") || !strings.Contains(out.Body.String(), "1 could not be archived.") {
		t.Fatal(out.Code, out.Body.String())
	}
	if !regexp.MustCompile(`value="` + stale + `"[^>]+checked`).MatchString(out.Body.String()) {
		t.Fatal("failed selection was lost")
	}
	if strings.Contains(out.Body.String(), `href="/sessions/`+first+`"`) {
		t.Fatal("archived session still in library")
	}
}
