package bwt

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Pacific is the timezone Bing Webmaster Tools reports in. Computing "today" in
// Japan would ask for a day the report does not have yet.
var Pacific = mustLoadPacific()

func mustLoadPacific() *time.Location {
	if loc, err := time.LoadLocation("America/Los_Angeles"); err == nil {
		return loc
	}
	return time.FixedZone("PST", -8*60*60) // no tzdata on this machine
}

// DefaultLagDays is how far back the most recent settled bucket is. Bing's
// traffic reports run about two days behind.
const DefaultLagDays = 2

// DateFormat is the format used on the command line and in output.
const DateFormat = "2006-01-02"

// DefaultPreset is the range used when none is given.
//
// It is wider than the equivalent Search Console default on purpose: Bing
// reports traffic in weekly buckets, so 28 days is only about four data points
// and a slow week reads as a collapse.
const DefaultPreset = "last_3m"

// presets are the named ranges accepted by --preset, resolved against the
// latest settled day (today in PT minus the lag).
var presets = map[string]func(latest time.Time) (time.Time, time.Time){
	"today":      func(l time.Time) (time.Time, time.Time) { t := TodayPT(); return t, t },
	"yesterday":  func(l time.Time) (time.Time, time.Time) { t := TodayPT().AddDate(0, 0, -1); return t, t },
	"latest":     func(l time.Time) (time.Time, time.Time) { return l, l },
	"last_7d":    func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, 0, -6), l },
	"last_28d":   func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, 0, -27), l },
	"last_30d":   func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, 0, -29), l },
	"last_90d":   func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, 0, -89), l },
	"last_3m":    func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, -3, 1), l },
	"last_6m":    func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, -6, 1), l },
	"last_12m":   func(l time.Time) (time.Time, time.Time) { return l.AddDate(0, -12, 1), l },
	"this_month": func(l time.Time) (time.Time, time.Time) { return monthStart(l), l },
	"last_month": func(l time.Time) (time.Time, time.Time) {
		start := monthStart(l).AddDate(0, -1, 0)
		return start, start.AddDate(0, 1, -1)
	},
	"all": func(l time.Time) (time.Time, time.Time) { return l.AddDate(-20, 0, 0), l },
}

// PresetNames lists the valid --preset values, sorted for help text.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for k := range presets {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// TodayPT is midnight today in the reporting timezone.
func TodayPT() time.Time {
	n := time.Now().In(Pacific)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, Pacific)
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, Pacific)
}

// DateRange is an inclusive [Start, End] range of days, in Pacific Time.
type DateRange struct {
	Start time.Time
	End   time.Time
}

func (r DateRange) StartString() string { return r.Start.Format(DateFormat) }
func (r DateRange) EndString() string   { return r.End.Format(DateFormat) }
func (r DateRange) String() string      { return r.StartString() + " 〜 " + r.EndString() }

// Days is the inclusive length of the range.
func (r DateRange) Days() int { return int(r.End.Sub(r.Start).Hours()/24) + 1 }

// Contains reports whether a report bucket falls inside the range. The bucket's
// timestamp is compared as a day in PT, so a bucket stamped at midnight PT
// belongs to that day and not to the previous one in UTC.
func (r DateRange) Contains(d Date) bool {
	if d.IsZero() {
		return false
	}
	day := d.Pacific().Format(DateFormat)
	return day >= r.StartString() && day <= r.EndString()
}

// RangeSpec is the raw, unresolved date selection from the command line.
type RangeSpec struct {
	Start   string // YYYY-MM-DD; wins over Preset
	End     string // YYYY-MM-DD
	Days    int    // last N days ending at End; wins over Preset
	Preset  string // see PresetNames
	LagDays int    // days of data-settling lag; DefaultLagDays if zero
}

// Resolve turns a RangeSpec into concrete dates.
//
// Precedence: explicit --start/--end, then --days, then --preset.
func Resolve(s RangeSpec) (DateRange, error) {
	if s.LagDays < 0 {
		return DateRange{}, fmt.Errorf("--lag は0以上にしてください")
	}
	latest := TodayPT().AddDate(0, 0, -s.LagDays)

	end := latest
	if s.End != "" {
		t, err := time.ParseInLocation(DateFormat, s.End, Pacific)
		if err != nil {
			return DateRange{}, fmt.Errorf("--end %q: YYYY-MM-DD 形式で指定してください", s.End)
		}
		end = t
	}

	var start time.Time
	switch {
	case s.Start != "":
		t, err := time.ParseInLocation(DateFormat, s.Start, Pacific)
		if err != nil {
			return DateRange{}, fmt.Errorf("--start %q: YYYY-MM-DD 形式で指定してください", s.Start)
		}
		start = t
	case s.Days > 0:
		start = end.AddDate(0, 0, -(s.Days - 1))
	default:
		name := s.Preset
		if name == "" {
			name = DefaultPreset
		}
		fn, ok := presets[strings.ToLower(name)]
		if !ok {
			return DateRange{}, fmt.Errorf("不明な --preset %q（有効: %s）", s.Preset, strings.Join(PresetNames(), ", "))
		}
		ps, pe := fn(latest)
		start = ps
		if s.End == "" {
			end = pe
		}
	}

	if start.After(end) {
		return DateRange{}, fmt.Errorf("開始日 %s が終了日 %s より後です", start.Format(DateFormat), end.Format(DateFormat))
	}
	return DateRange{Start: start, End: end}, nil
}

// Comparison returns the range to compare r against.
//
//	"previous" — the equally long window immediately before r.
//	"year"     — the same window 364 days earlier, which keeps weekdays aligned.
func Comparison(r DateRange, mode string) (DateRange, error) {
	switch strings.ToLower(mode) {
	case "previous", "prev":
		n := r.Days()
		end := r.Start.AddDate(0, 0, -1)
		return DateRange{Start: end.AddDate(0, 0, -(n - 1)), End: end}, nil
	case "year", "yoy":
		return DateRange{Start: r.Start.AddDate(0, 0, -364), End: r.End.AddDate(0, 0, -364)}, nil
	default:
		return DateRange{}, fmt.Errorf("不明な --compare %q（有効: previous, year）", mode)
	}
}
