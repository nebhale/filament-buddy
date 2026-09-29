package buddy

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

//go:embed web/*
var assets embed.FS

type Web struct {
	s    *Service
	t    *template.Template
	csrf string
}
type Setup struct {
	Printer  Printer
	Snippets Snippets
}
type Page struct {
	BulkAction                                   string
	Bulk                                         *bulkReply
	Selected                                     map[string]bool
	ReturnURL                                    string
	Live                                         bool
	View, Title, CSRF, Error, SpoolError, Filter string
	Printers                                     []PrinterStatus
	Sessions                                     []Session
	Session                                      Session
	Spools                                       []Spool
	Setup                                        []Setup
	Archived                                     bool
	Page, Previous                               int
	More                                         bool
}

var colorPattern = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func NewWeb(s *Service) (http.Handler, error) {
	w := &Web{s: s, csrf: rand.Text()}
	funcs := template.FuncMap{
		"printerName":       func(id string) string { return printerDisplayName(s.Config, id) },
		"markerDescription": func(raw string) string { return markerDescription(s.Config, raw) },
		"next":              func(n int) int { return n + 1 },
		"grams":             func(n int64) string { return fmt.Sprintf("%.3f", float64(n)/1000) }, "weight": func(n *int64) string {
			if n == nil {
				return "—"
			}
			return fmt.Sprintf("%.3f g", float64(*n)/1000)
		},
		"value": func(n *int64) string {
			if n == nil {
				return ""
			}
			return fmt.Sprintf("%.3f", float64(*n)/1000)
		},
		"hasSpool": func(id int, spools []Spool) bool {
			for _, v := range spools {
				if v.ID == id {
					return true
				}
			}
			return false
		},
		"label": func(id int, spools []Spool) string {
			if id == 0 {
				return "No spool"
			}
			for _, v := range spools {
				if v.ID == id {
					return v.Label
				}
			}
			return fmt.Sprintf("Spool #%d (unavailable)", id)
		},
		"color": func(id int, spools []Spool) string {
			for _, v := range spools {
				if v.ID == id && colorPattern.MatchString(v.Color) {
					return "#" + v.Color
				}
			}
			return "#9a8daf"
		},
		"pending": func(state string) bool {
			return state == "uncertain" || state == "conflict" || state == "pending" || state == "inflight"
		},
	}
	var err error
	w.t, err = template.New("pages.html").Funcs(funcs).ParseFS(assets, "web/pages.html")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.dashboard)
	mux.HandleFunc("GET /setup", w.setup)
	mux.HandleFunc("GET /api/live", w.live)
	mux.HandleFunc("GET /api/events", func(rw http.ResponseWriter, r *http.Request) { serveEvents(rw, r, &s.Store.changes, w.csrf) })
	mux.HandleFunc("POST /sessions/bulk/archive", w.bulkSessions)
	mux.HandleFunc("POST /sessions/bulk/restore", w.bulkSessions)
	mux.HandleFunc("GET /sessions/{id}", w.session)
	mux.HandleFunc("POST /sessions/{id}/name", w.renameSession)
	mux.HandleFunc("GET /api/status", func(rw http.ResponseWriter, r *http.Request) {
		p, e := s.Status()
		if e != nil {
			w.fail(rw, e)
			return
		}
		writeJSON(rw, p)
	})
	mux.HandleFunc("GET /api/sessions/{id}", func(rw http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Get(r.PathValue("id"))
		if e != nil {
			w.fail(rw, e)
			return
		}
		writeJSON(rw, v)
	})
	mux.HandleFunc("GET /api/spools", func(rw http.ResponseWriter, r *http.Request) {
		v, e := s.Store.Spools()
		if e != nil {
			w.fail(rw, e)
			return
		}
		writeJSON(rw, v)
	})
	mux.HandleFunc("POST /spools/refresh", func(rw http.ResponseWriter, r *http.Request) {
		if err := s.Spoolman.Refresh(r.Context()); err != nil {
			w.fail(rw, err)
			return
		}
		http.Redirect(rw, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /sessions/{id}/{action}", w.mutate)
	static, _ := fs.Sub(assets, "web")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	secured := w.security(jsonActions(mux))
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			if err := s.Store.db.PingContext(r.Context()); err != nil {
				http.Error(rw, "unhealthy", 503)
				return
			}
			rw.Write([]byte("ok\n"))
			return
		}
		secured.ServeHTTP(rw, r)
	}), nil
}
func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(v)
}
func (w *Web) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		rw.Header().Set("Referrer-Policy", "same-origin")
		rw.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		rw.Header().Set("Cache-Control", "no-store")
		if w.s.Config.AuthUser != "" {
			u, p, ok := r.BasicAuth()
			a, b := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
			x, y := sha256.Sum256([]byte(w.s.Config.AuthUser)), sha256.Sum256([]byte(w.s.Config.AuthPassword))
			if !ok || subtle.ConstantTimeCompare(a[:], x[:])&subtle.ConstantTimeCompare(b[:], y[:]) != 1 {
				rw.Header().Set("WWW-Authenticate", `Basic realm="Filament Buddy", charset="UTF-8"`)
				http.Error(rw, "Authentication required", 401)
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			r.Body = http.MaxBytesReader(rw, r.Body, 64<<10)
			if err := r.ParseForm(); err != nil {
				http.Error(rw, "Invalid form", 400)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(w.csrf)) != 1 {
				http.Error(rw, "Page expired. Reload and try again.", 403)
				return
			}
		}
		next.ServeHTTP(rw, r)
	})
}
func (w *Web) render(rw http.ResponseWriter, status int, p Page) {
	p.CSRF = w.csrf
	p.SpoolError = w.s.Spoolman.Status()
	if _, ok := rw.(*actionResponse); ok {
		renderJSONError(rw, status, p.Error)
		return
	}
	if live, ok := rw.(*liveResponse); ok {
		if status != 200 {
			renderJSONError(rw, status, p.Error)
			return
		}
		w.renderLive(live, p)
		return
	}
	var b bytes.Buffer
	if err := w.t.ExecuteTemplate(&b, "layout", p); err != nil {
		slog.Error("render", "error", err)
		http.Error(rw, "Unable to render page", 500)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(status)
	rw.Write(b.Bytes())
}
func (w *Web) fail(rw http.ResponseWriter, err error) {
	status := 500
	message := "Unable to complete request. Check service logs."
	if errors.Is(err, ErrInvalidDisplayName) {
		status, message = 400, err.Error()
	} else if errors.Is(err, ErrDisplayNameConflict) {
		status, message = 409, err.Error()
	} else if errors.Is(err, ErrNotFound) {
		status = 404
		message = "Session or section not found."
	} else if errors.Is(err, ErrConflict) {
		status = 409
		message = err.Error()
	} else {
		slog.Error("web request", "error", err)
	}
	w.render(rw, status, Page{View: "error", Title: "Unable to complete request", Error: message})
}
func (w *Web) dashboard(rw http.ResponseWriter, r *http.Request) {
	p, err := w.libraryPage(r.URL.Query())
	if err != nil {
		w.fail(rw, err)
		return
	}
	w.render(rw, 200, p)
}
func (w *Web) session(rw http.ResponseWriter, r *http.Request) {
	ss, err := w.s.Store.Get(r.PathValue("id"))
	if err != nil {
		w.fail(rw, err)
		return
	}
	spools, err := w.s.Store.Spools()
	if err != nil {
		w.fail(rw, err)
		return
	}
	w.render(rw, 200, Page{View: "session", Title: ss.Title(), Session: ss, Spools: spools})
}
func (w *Web) setup(rw http.ResponseWriter, r *http.Request) {
	p := Page{View: "setup", Title: "Printer setup"}
	for _, v := range w.s.Config.Printers {
		p.Setup = append(p.Setup, Setup{v, GCode(w.s.Config, v)})
	}
	w.render(rw, 200, p)
}
func parseMG(v string) (*int64, error) {
	if v == "" {
		return nil, nil
	}
	if !regexp.MustCompile(`^\d{1,6}(\.\d{1,3})?$`).MatchString(v) {
		return nil, ErrConflict
	}
	parts := strings.SplitN(v, ".", 2)
	whole, _ := strconv.ParseInt(parts[0], 10, 64)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", 3-len(fraction))
	f, _ := strconv.ParseInt(fraction, 10, 64)
	mg := whole*1000 + f
	if mg > MaxMilligrams {
		return nil, ErrConflict
	}
	return &mg, nil
}
func (w *Web) mutate(rw http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	rev, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil || rev < 0 {
		w.fail(rw, ErrConflict)
		return
	}
	sid := r.PostForm.Get("section")
	switch action {
	case "close":
		err = w.s.CloseSession(id, rev)
	case "archive", "restore":
		err = w.s.Store.Archive(id, rev, action == "archive")
	case "plan":
		err = w.s.Store.Plan(id, rev, r.PostForm.Get("move"), sid)
	case "spool":
		var n int
		n, err = strconv.Atoi(r.PostForm.Get("spool"))
		if err == nil && n < 0 {
			err = ErrConflict
		}
		if err == nil && n != 0 {
			var spools []Spool
			spools, err = w.s.Store.Spools()
			found := false
			for _, sp := range spools {
				if sp.ID == n {
					found = true
				}
			}
			if !found {
				err = fmt.Errorf("%w: refresh the spool catalog before assigning an unknown spool", ErrConflict)
			}
		}
		if err == nil {
			if values, ok := r.PostForm["expected_spool"]; ok && strings.Contains(r.Header.Get("Accept"), "application/json") {
				expected, e := strconv.Atoi(values[0])
				if e != nil || expected < 0 {
					err = ErrConflict
				} else {
					err = w.s.Store.SetSectionExpected(id, sid, n, nil, false, SectionExpectation{Spool: expected})
				}
			} else {
				err = w.s.Store.SetSection(id, sid, rev, n, nil, false)
			}
		}
	case "weight":
		var mg *int64
		mg, err = parseMG(r.PostForm.Get("grams"))
		if err == nil {
			if values, ok := r.PostForm["expected_override"]; ok && strings.Contains(r.Header.Get("Accept"), "application/json") {
				expected, e := parseMG(values[0])
				if e != nil {
					err = ErrConflict
				} else {
					err = w.s.Store.SetSectionExpected(id, sid, 0, mg, true, SectionExpectation{Override: expected})
				}
			} else {
				err = w.s.Store.SetSection(id, sid, rev, 0, mg, true)
			}
		}
	case "resolve":
		err = w.s.Store.Resolve(id, sid, r.PostForm.Get("operation"), r.PostForm.Get("resolution"), rev)
	default:
		err = ErrNotFound
	}
	if err != nil {
		w.fail(rw, err)
		return
	}
	http.Redirect(rw, r, "/sessions/"+id, http.StatusSeeOther)
}
