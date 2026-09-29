package buddy

import (
	"html"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func visiblePageText(body string) string {
	return html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(body, ""))
}

func TestPrinterDisplayFallbacks(t *testing.T) {
	c := Config{Printers: []Printer{{ID: "c1", Name: "Prusa CORE One"}, {ID: "c2", Name: "c2"}, {ID: "c3"}}}
	for id, want := range map[string]string{"c1": "Prusa CORE One", "c2": "Unnamed printer", "c3": "Unnamed printer", "removed": "Unknown printer"} {
		if got := printerDisplayName(c, id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

func TestMarkerDescriptions(t *testing.T) {
	c := Config{Printers: []Printer{{ID: "c1", Name: "Prusa CORE One"}}}
	for raw, want := range map[string]string{
		"M118 FB1 START c1 c1 fixture": "START · Prusa CORE One · c1 fixture",
		"invalid":                      "Unrecognized marker",
		"M118 FB1 CHANGE c1 2 12000":   "CHANGE · Prusa CORE One · Section 2 · 12.000 g",
		"M118 FB1 STOP c1 3 4500":      "STOP · Prusa CORE One · Section 3 · 4.500 g",
	} {
		if got := markerDescription(c, raw); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}

func TestVisiblePrinterNames(t *testing.T) {
	s := testStore(t)
	id, _ := completed(t, s)
	ss := get(t, s, id)
	ss.History = append(ss.History,
		History{At: time.Now(), Action: "conflicting marker", Detail: "M118 FB1 CHANGE c1 1 9000"},
		History{At: time.Now(), Action: "out-of-order end", Detail: "M118 FB1 STOP c1 2 1000"})
	must(t, s.save(s.db, &ss))
	c := testConfig()
	c.Printers[0].Name = "Prusa <CORE> One"
	h, err := NewWeb(NewService(c, s))
	must(t, err)
	for _, path := range []string{"/", "/sessions/" + id} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		text := visiblePageText(w.Body.String())
		if w.Code != 200 || !strings.Contains(text, "Prusa <CORE> One") || strings.Contains(text, "c1") {
			t.Fatalf("%s: status %d, text %s", path, w.Code, text)
		}
		if strings.Contains(w.Body.String(), "Prusa <CORE> One") {
			t.Fatal("printer name was not escaped")
		}
	}
	assignUnassignedSpools(t, s, id)
	must(t, s.Archive(id, -1, true))
	archived := httptest.NewRecorder()
	h.ServeHTTP(archived, httptest.NewRequest("GET", "/?archived=true", nil))
	text := visiblePageText(archived.Body.String())
	if strings.Contains(text, "c1") || !strings.Contains(text, "Prusa <CORE> One") {
		t.Fatal("archived list did not use printer names")
	}
	setup := httptest.NewRecorder()
	h.ServeHTTP(setup, httptest.NewRequest("GET", "/setup", nil))
	if !strings.Contains(visiblePageText(setup.Body.String()), "M118 FB1 START c1 ") {
		t.Fatal("G-code lost the printer ID")
	}
	saved := get(t, s, id)
	if saved.PrinterID != "c1" || saved.Events[0].Raw != "M118 FB1 START c1 A month ago" || saved.History[len(ss.History)-1].Detail != "M118 FB1 STOP c1 2 1000" {
		t.Fatal("stored protocol data changed")
	}
}
