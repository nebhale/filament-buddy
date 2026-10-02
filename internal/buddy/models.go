package buddy

import "time"

type Session struct {
	ID          string
	PrinterID   string
	Name        string
	DisplayName string `json:",omitempty"`
	State       string
	CloseReason string
	OpenedAt    time.Time
	ClosedAt    *time.Time
	Recovered   bool
	Archived    bool
	Revision    int
	Current     int
	Sections    []Section
	Events      []Event
	History     []History
}
type Section struct {
	ID         string
	Number     int
	State      string
	SpoolID    int
	ReportedMG *int64
	Applied    map[int]int64
	Operations []Operation
}
type Operation struct {
	ID          string
	SpoolID     int
	DeltaMG     int64
	State       string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	NextAttempt time.Time
	Attempts    int
	BeforeMG    *int64
}
type History struct {
	At     time.Time
	Action string
	Detail string
}
type Spool struct {
	ID          int
	Label       string
	Color       string
	Archived    bool
	UsedMG      int64
	RemainingMG *int64
	UpdatedAt   time.Time
}

func (s Section) Weight() *int64 {
	if s.State == "planned" || s.State == "unreached" {
		return nil
	}
	return s.ReportedMG
}
func (s Section) SyncStatus() string {
	for _, state := range []string{"uncertain", "conflict", "inflight", "pending"} {
		for _, op := range s.Operations {
			if op.State == state {
				return state
			}
		}
	}
	if s.SpoolID == 0 {
		return "unassigned"
	}
	if s.Weight() == nil {
		return "awaiting weight"
	}
	return "synced"
}
func (s Session) TotalMG() int64 {
	var n int64
	for _, v := range s.Sections {
		if w := v.Weight(); w != nil {
			n += *w
		}
	}
	return n
}
func (s Session) UnassignedSections() int {
	var n int
	for _, section := range s.Sections {
		if section.SpoolID == 0 {
			n++
		}
	}
	return n
}
func (s *Session) audit(action, detail string) {
	s.History = append(s.History, History{time.Now().UTC(), action, detail})
}
