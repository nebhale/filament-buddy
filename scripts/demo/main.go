// A local, synthetic UI preview. No printer or Spoolman connections are made.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/nebhale/filament-buddy/internal/buddy"
)

func main() {
	dir, err := os.MkdirTemp("", "filament-buddy-demo-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := buddy.OpenStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	var c buddy.Config
	c.HTTP.Address = "127.0.0.1:8091"
	c.Metrics.Address = ":8514"
	c.Metrics.AdvertisedHost = "192.168.1.50"
	c.Metrics.AdvertisedPort = 8514
	c.Spoolman.URL = "http://127.0.0.1:1"
	c.Spoolman.Timeout = time.Second
	c.Printers = []buddy.Printer{{ID: "c1", Name: "Prusa CORE One"}, {ID: "mini", Name: "Prusa MINI+"}}
	s.CacheSpools([]buddy.Spool{{ID: 7, Label: "#7 · Prusament · Galaxy Purple PLA", Color: "8055aa"}, {ID: 12, Label: "#12 · Polymaker · Cotton White PLA", Color: "eee8db"}, {ID: 19, Label: "#19 · Prusament · Azure Blue PETG", Color: "389bbf"}})
	at := time.Now().Add(-3 * time.Hour)
	id := ""
	for i, raw := range []string{"M118 FB1 START c1 Mountain landscape", "M118 FB1 CHANGE c1 1 24350", "M118 FB1 CHANGE c1 2 8500"} {
		e, _ := buddy.ParseMarker(raw)
		e.ReceivedAt = at.Add(time.Duration(i) * time.Hour)
		id, err = s.Apply(e)
		if err != nil {
			log.Fatal(err)
		}
	}
	ss, _ := s.Get(id)
	s.SetSection(id, ss.Sections[0].ID, -1, 7, nil, false)
	s.SetSection(id, ss.Sections[1].ID, -1, 12, nil, false)
	s.Plan(id, -1, "add", "")
	ss, _ = s.Get(id)
	s.SetSection(id, ss.Sections[3].ID, -1, 7, nil, false)
	for i, name := range []string{"Desk organizer", "Cable clips", "Plant marker set"} {
		e, _ := buddy.ParseMarker("M118 FB1 START mini " + name)
		e.ReceivedAt = at.Add(-time.Duration(i+1) * 24 * time.Hour)
		old, _ := s.Apply(e)
		e, _ = buddy.ParseMarker(fmt.Sprintf("M118 FB1 STOP mini 1 %d", 12000+i*7500))
		e.ReceivedAt = at.Add(-time.Duration(i+1)*24*time.Hour + time.Hour)
		s.Apply(e)
		v, _ := s.Get(old)
		s.SetSection(old, v.Sections[0].ID, -1, 19, nil, false)
	}
	service := buddy.NewService(c, s)
	h, err := buddy.NewWeb(service)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /demo/marker", func(w http.ResponseWriter, r *http.Request) {
		e, err := buddy.ParseMarker("M118 FB1 " + r.FormValue("marker"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		e.ReceivedAt = time.Now().UTC()
		if err = service.Handle(e); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /demo/catalog", func(w http.ResponseWriter, r *http.Request) {
		var catalog []buddy.Spool
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&catalog); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.CacheSpools(catalog); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	application := newDemoApplication(h)
	mux.HandleFunc("POST /demo/restart", func(w http.ResponseWriter, r *http.Request) {
		next, err := buddy.NewWeb(service)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		application.reset(next)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", application)
	port := os.Getenv("BUDDY_DEMO_PORT")
	if port == "" {
		port = "8091"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Demo: http://%s  Session: /sessions/%s\n", listener.Addr(), id)
	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		mux.ServeHTTP(w, r)
	})))
}
