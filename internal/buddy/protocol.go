package buddy

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxSection = 9999
const MaxMilligrams int64 = 999999999

var metricPattern = regexp.MustCompile(`(?:^|\s)gcode(?:,[^\s]+)?\s+v="((?:\\.|[^"\\])*)"`)

type Event struct {
	Kind       string
	PrinterID  string
	Name       string
	Section    int
	Milligrams int64
	Raw        string
	SourceIP   string
	ReceivedAt time.Time
}

func ParsePacket(data []byte, source string, at time.Time) []Event {
	var out []Event
	for _, line := range strings.Split(string(data), "\n") {
		command := strings.TrimSpace(line)
		if !strings.HasPrefix(command, "M118 FB1 ") {
			m := metricPattern.FindStringSubmatch(command)
			if m == nil {
				continue
			}
			var err error
			command, err = strconv.Unquote(`"` + m[1] + `"`)
			if err != nil {
				continue
			}
		}
		e, err := ParseMarker(command)
		if err == nil {
			e.SourceIP = source
			e.ReceivedAt = at.UTC()
			out = append(out, e)
		}
	}
	return out
}
func ParseMarker(raw string) (Event, error) {
	e := Event{Raw: raw}
	bad := errors.New("invalid Filament Buddy marker")
	if len(raw) > 47 || !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 || !strings.HasPrefix(raw, "M118 FB1 ") {
		return e, bad
	}
	p := strings.SplitN(strings.TrimPrefix(raw, "M118 FB1 "), " ", 3)
	if len(p) != 3 || !printerIDPattern.MatchString(p[1]) {
		return e, bad
	}
	e.Kind, e.PrinterID = p[0], p[1]
	if e.Kind == "START" {
		e.Name = strings.TrimSpace(p[2])
		if e.Name == "" {
			return e, bad
		}
		return e, nil
	}
	if e.Kind != "CHANGE" && e.Kind != "STOP" {
		return e, bad
	}
	v := strings.Fields(p[2])
	if len(v) != 2 {
		return e, bad
	}
	var err error
	e.Section, err = strconv.Atoi(v[0])
	if err != nil || e.Section < 1 || e.Section > MaxSection || (e.Kind == "CHANGE" && e.Section == MaxSection) {
		return e, bad
	}
	e.Milligrams, err = strconv.ParseInt(v[1], 10, 64)
	if err != nil || e.Milligrams < 0 || e.Milligrams > MaxMilligrams {
		return e, bad
	}
	return e, nil
}

type Snippets struct{ Start, Change, Stop string }

func GCode(c Config, p Printer) Snippets {
	block := func(kind, payload string) string {
		m := "M118 FB1 " + kind + " " + p.ID + " " + payload
		// Every boundary carries irreplaceable accounting data. Spread retries
		// beyond the firmware's one-second metrics batching interval so one
		// dropped UDP packet cannot discard the entire burst.
		return "M331 gcode\n" + m + "\nG4 P1100\n" + m + "\nG4 P1100\n" + m + "\nM332 gcode"
	}
	label := func(name, body string) string {
		return "; BEGIN Filament Buddy: " + name + " (" + p.ID + ")\n" + body + "\n; END Filament Buddy: " + name + " (" + p.ID + ")"
	}
	// The cumulative value is rounded before subtraction, so segment rounding
	// sums to the same rounded total. Global variables are evaluated by the slicer.
	boundary := "{local fb_now = int(round(extruded_weight_total * 1000))}\n{local fb_used = fb_now - fb_previous}\n"
	payload := "{fb_section} {fb_used}"
	// ASCII makes the character limit equal the firmware's 47-byte limit.
	name := fmt.Sprintf("{if input_filename_base =~ /^[a-zA-Z0-9 _.-]{1,%d}$/}{input_filename_base}{else}Print{endif}", 47-len("M118 FB1 START "+p.ID+" "))
	return Snippets{
		Start:  label("start session", fmt.Sprintf("M334 %s %d 13514\n", c.Metrics.AdvertisedHost, c.Metrics.AdvertisedPort)+"{global fb_section = 1}\n{global fb_previous = 0}\n"+block("START", name)),
		Change: label("complete section", "M400\n"+boundary+block("CHANGE", payload)+"\n{fb_previous = fb_now}\n{fb_section = fb_section + 1}"),
		Stop:   label("close session", "M400\n"+boundary+block("STOP", payload)),
	}
}
