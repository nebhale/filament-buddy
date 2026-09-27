package buddy

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	var c Config
	c.HTTP.Address = ":8080"
	c.Metrics.Address = ":8514"
	c.Metrics.AdvertisedHost = "192.168.1.50"
	c.Metrics.AdvertisedPort = 8514
	c.Spoolman.URL = "http://localhost:7912"
	c.Spoolman.Timeout = time.Second
	c.Printers = []Printer{{ID: "c1", Name: "CORE One"}}
	return c
}
func TestProtocol(t *testing.T) {
	for _, raw := range []string{"M118 FB1 START c1 Test print", "M118 FB1 CHANGE c1 1 12000", "M118 FB1 STOP abcdefghi 9999 999999999"} {
		e, err := ParseMarker(raw)
		if err != nil {
			t.Fatal(err)
		}
		packet := fmt.Sprintf(`<14>1 test gcode v=%q`, raw)
		got := ParsePacket([]byte(packet), "127.0.0.1", time.Now())
		if len(got) != 1 || got[0].Raw != e.Raw {
			t.Fatalf("packet: %+v", got)
		}
	}
	for _, raw := range []string{"M118 FB1 CHANGE c1 0 2", "M118 FB1 STOP c1 1 -2", "M118 FB1 CHANGE c1 1 1.5", "M118 FB1 CHANGE c1 10000 2", "M118 SB1 LAYER c1 1", "M118 FB1 START c1 ", "M118 FB1 START c1 " + strings.Repeat("a", 48)} {
		if _, err := ParseMarker(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func FuzzParsePacket(f *testing.F) {
	f.Add([]byte(`gcode v="M118 FB1 CHANGE c1 1 123"`))
	f.Fuzz(func(t *testing.T, b []byte) { ParsePacket(b, "127.0.0.1", time.Now()) })
}
func TestConfig(t *testing.T) {
	base := `metrics:
  advertised_host: 192.168.1.50
spoolman:
  url: http://spoolman:8000
printers:
  - id: c1
`
	c, err := DecodeConfig(strings.NewReader(base))
	if err != nil || c.Metrics.AdvertisedPort != 8514 {
		t.Fatalf("%+v %v", c, err)
	}
	for _, suffix := range []string{"unknown: true\n", "---\nfoo: bar\n"} {
		if _, err = DecodeConfig(strings.NewReader(base + suffix)); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	bad := strings.Replace(base, "advertised_host: 192.168.1.50", "advertised_host: 192.168.1.50\n  advertised_port: 65536", 1)
	if _, err = DecodeConfig(strings.NewReader(bad)); err == nil {
		t.Fatal("invalid port accepted")
	}
}
func cubeSTL() string {
	v := [][3]int{{0, 0, 0}, {10, 0, 0}, {10, 10, 0}, {0, 10, 0}, {0, 0, 4}, {10, 0, 4}, {10, 10, 4}, {0, 10, 4}}
	faces := [][3]int{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	var b strings.Builder
	b.WriteString("solid cube\n")
	for _, f := range faces {
		b.WriteString("facet normal 0 0 0\nouter loop\n")
		for _, i := range f {
			fmt.Fprintf(&b, "vertex %d %d %d\n", v[i][0], v[i][1], v[i][2])
		}
		b.WriteString("endloop\nendfacet\n")
	}
	b.WriteString("endsolid cube\n")
	return b.String()
}
func TestPrusaSlicer(t *testing.T) {
	binary := os.Getenv("PRUSA_SLICER")
	if binary == "" {
		binary = "/Applications/PrusaSlicer.app/Contents/MacOS/PrusaSlicer"
	}
	if _, err := os.Stat(binary); err != nil {
		if os.Getenv("REQUIRE_SLICER") == "1" {
			t.Fatal(err)
		}
		t.Skip("set PRUSA_SLICER to validate generated snippets")
	}
	for _, name := range []string{"cube", strings.Repeat("a", 22), strings.Repeat("a", 23), strings.Repeat("long", 20), "紫色"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			model := filepath.Join(dir, name+".stl")
			if err := os.WriteFile(model, []byte(cubeSTL()), 0600); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(dir, name+".3mf")
			if out, err := exec.Command(binary, "--export-3mf", "--output", project, model).CombinedOutput(); err != nil {
				t.Fatalf("3mf export: %v %s", err, out)
			}
			addColorChanges(t, project)
			c := testConfig()
			c.Printers[0].ID = "abcdefghi"
			snippets := GCode(c, c.Printers[0])
			output := filepath.Join(dir, "print.gcode")
			args := []string{"--export-gcode", "--output", output, "--filament-density", "1.24", "--filament-diameter", "1.75", "--layer-height", "0.2", "--first-layer-height", "0.2", "--colorprint-heights", "1,2", "--start-gcode", snippets.Start, "--color-change-gcode", snippets.Change + "\nM600", "--end-gcode", snippets.Stop + "\n; TEST_TOTAL {int(round(extruded_weight_total * 1000))}", project}
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("slicing failed: %v\n%s", err, out)
			}
			gcode, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var events []Event
			var last string
			for _, line := range strings.Split(string(gcode), "\n") {
				if !strings.HasPrefix(line, "M118 FB1 ") {
					continue
				}
				if len(line) > 47 {
					t.Fatalf("oversized marker %q", line)
				}
				e, err := ParseMarker(line)
				if err != nil {
					t.Fatal(err)
				}
				if line != last {
					events = append(events, e)
				}
				last = line
			}
			if len(events) != 4 || events[0].Kind != "START" || events[1].Kind != "CHANGE" || events[2].Kind != "CHANGE" || events[3].Kind != "STOP" {
				t.Fatalf("expected start/change/change/end; got %+v\n%s", events, out)
			}
			wantName := name
			if len(name) > 22 || name == "紫色" {
				wantName = "Print"
			}
			if events[0].Name != wantName {
				t.Fatalf("session name %q, want %q", events[0].Name, wantName)
			}
			var total int64
			for i, e := range events[1:] {
				if e.Section != i+1 || e.Milligrams <= 0 {
					t.Fatalf("invalid section %+v", e)
				}
				total += e.Milligrams
			}
			if total <= 0 {
				t.Fatal("zero total")
			}
			for _, line := range strings.Split(string(gcode), "\n") {
				if strings.HasPrefix(line, "; TEST_TOTAL ") {
					var want int64
					fmt.Sscanf(line, "; TEST_TOTAL %d", &want)
					if total != want {
						t.Fatalf("sections sum %d, slicer total %d", total, want)
					}
				}
			}
			if strings.Count(string(gcode), "\nM600\n") != 2 {
				t.Fatal("existing color change commands lost")
			}

		})
	}
}

func addColorChanges(t *testing.T, path string) {
	t.Helper()
	r, err := zip.OpenReader(path)
	must(t, err)
	output := path + ".tmp"
	f, err := os.Create(output)
	must(t, err)
	w := zip.NewWriter(f)
	for _, entry := range r.File {
		if entry.Name == "Metadata/Prusa_Slicer_custom_gcode_per_print_z.xml" {
			continue
		}
		src, err := entry.Open()
		must(t, err)
		dst, err := w.Create(entry.Name)
		must(t, err)
		_, err = io.Copy(dst, src)
		must(t, err)
		src.Close()
	}
	dst, err := w.Create("Metadata/Prusa_Slicer_custom_gcode_per_print_z.xml")
	must(t, err)
	_, err = io.WriteString(dst, `<?xml version="1.0"?><custom_gcodes_per_print_z><code print_z="1" type="0" extruder="1" color="#FF0000" extra=""/><code print_z="2" type="0" extruder="1" color="#0000FF" extra=""/><mode value="SingleExtruder"/></custom_gcodes_per_print_z>`)
	must(t, err)
	must(t, w.Close())
	must(t, f.Close())
	must(t, r.Close())
	must(t, os.Rename(output, path))
}

func TestBoundaryRetryTiming(t *testing.T) {
	c := testConfig()
	snippets := GCode(c, c.Printers[0])
	for _, block := range []string{snippets.Start, snippets.Change, snippets.Stop} {
		if strings.Count(block, "G4 P1100") != 2 || strings.Count(block, "M118 FB1") != 3 {
			t.Fatal("boundary retries must span firmware metrics batches")
		}
	}
}

func TestSharedBoundaryPacket(t *testing.T) {
	// A layer snapshot and the following color boundary can share a firmware
	// packet. Each app must ignore the other's markers without losing its own.
	packet := `<14>1 - printer buddy - - - msg=123,tm=456,v=4 gcode v="M118 SB1 LAYER c1 3" 0
gcode v="G4P100" 100
gcode v="M118 SB1 LAYER c1 3" 100000
gcode v="M332 gcode" 100100
gcode v="M118 FB1 CHANGE c1 1 1445" 200000
gcode v="G4P100" 200100
gcode v="M118 FB1 CHANGE c1 1 1445" 300000
gcode v="M332 gcode" 300100
`
	got := ParsePacket([]byte(packet), "172.21.0.1", time.Now())
	if len(got) != 2 {
		t.Fatalf("expected both color-change copies, got %+v", got)
	}
	for _, e := range got {
		if e.Kind != "CHANGE" || e.PrinterID != "c1" || e.Section != 1 || e.Milligrams != 1445 {
			t.Fatalf("incorrect color boundary: %+v", e)
		}
	}
}

func TestRejectShortMarkers(t *testing.T) {
	for _, raw := range []string{"M118 FB1 S c1 Test print", "M118 FB1 C c1 1 12000", "M118 FB1 E c1 1 12000"} {
		if _, err := ParseMarker(raw); err == nil {
			t.Fatalf("accepted obsolete marker %q", raw)
		}
		for _, packet := range []string{raw, fmt.Sprintf(`gcode v=%q`, raw)} {
			if events := ParsePacket([]byte(packet), "127.0.0.1", time.Now()); len(events) != 0 {
				t.Fatalf("received obsolete marker: %+v", events)
			}
		}
	}
}
