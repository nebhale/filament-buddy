// A local, synthetic UI preview. No printer or Spoolman connections are made.
package main

import (
	"fmt"
	"log"
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
	h, err := buddy.NewWeb(buddy.NewService(c, s))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Demo: http://127.0.0.1:8091  Session: /sessions/" + id)
	log.Fatal(http.ListenAndServe(c.HTTP.Address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})))
}
