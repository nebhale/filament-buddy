package buddy

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebSecurityAndActions(t *testing.T) {
	s := testStore(t)
	id, sid := completed(t, s)
	must(t, s.CacheSpools([]Spool{{ID: 1, Label: "<script>unsafe</script> Purple", Color: "bad"}}))
	c := testConfig()
	c.AuthUser = "buddy"
	c.AuthPassword = "password"
	service := NewService(c, s)
	h, err := NewWeb(service)
	must(t, err)
	request := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			r.SetBasicAuth("buddy", "password")
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		return rw
	}
	if r := request("GET", "/", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := request("GET", "/healthz", "", false); r.Code != 200 {
		t.Fatal(r.Code)
	}
	for _, path := range []string{"/", "/setup", "/sessions/" + id, "/api/status", "/api/spools", "/api/sessions/" + id} {
		r := request("GET", path, "", true)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
		if r.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
	r := request("GET", "/sessions/"+id, "", true)
	if strings.Contains(r.Body.String(), "<script>unsafe") {
		t.Fatal("unescaped spool label")
	}
	re := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	token := re.FindStringSubmatch(r.Body.String())[1]
	data := url.Values{"csrf": {token}, "revision": {strconvI(get(t, s, id).Revision)}, "section": {sid}, "spool": {"1"}}
	if r = request("POST", "/sessions/"+id+"/spool", data.Encode(), true); r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	if get(t, s, id).Sections[0].SpoolID != 1 {
		t.Fatal("assignment failed")
	}
	if r = request("POST", "/sessions/"+id+"/spool", data.Encode(), true); r.Code != 409 {
		t.Fatal("stale form accepted")
	}
	data.Set("csrf", "wrong")
	if r = request("POST", "/sessions/"+id+"/spool", data.Encode(), true); r.Code != 403 {
		t.Fatal("missing CSRF protection")
	}
	if r = request("POST", "/sessions", "", true); r.Code == 200 || r.Code == 201 {
		t.Fatal("manual start endpoint exists")
	}
}
func strconvI(n int) string { return strconv.Itoa(n) }
func TestUDPReceiver(t *testing.T) {
	s := testStore(t)
	service := NewService(testConfig(), s)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	must(t, err)
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.ServeUDP(ctx, conn) }()
	sender, err := net.Dial("udp", conn.LocalAddr().String())
	must(t, err)
	defer sender.Close()
	_, err = io.WriteString(sender, "M118 SB1 START c1 Other app\nM118 FB1 START c1 UDP print")
	must(t, err)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.List("", false, 0)
		must(t, err)
		if len(rows) == 1 {
			cancel()
			conn.Close()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("UDP marker not received")
}
