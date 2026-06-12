package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/filerename/filerename/pkg/types"
)

type DateTimeRule struct {
	cfg *types.DateTimeConfig
}

func NewDateTimeRule(cfg *types.DateTimeConfig) *DateTimeRule {
	if cfg.Format == "" { cfg.Format = "2006-01-02_150405" }
	if cfg.Position == "" { cfg.Position = "prefix" }
	if cfg.Separator == "" { cfg.Separator = "_" }
	if cfg.Source == "" { cfg.Source = "auto" }
	return &DateTimeRule{cfg: cfg}
}

func (r *DateTimeRule) Name() string { return "datetime" }

func (r *DateTimeRule) Apply(f *types.FileInfo, index int) (string, bool, error) {
	if !r.cfg.Enabled {
		return "", false, nil
	}

	t, err := r.extractTime(f)
	if err != nil {
		return "", false, nil
	}
	if t.IsZero() {
		return "", false, nil
	}

	formatted := t.Format(r.convertFormat(r.cfg.Format))
	base := f.Name

	switch r.cfg.Position {
	case "suffix":
		return base + r.cfg.Separator + formatted, true, nil
	case "replace":
		return formatted, true, nil
	case "both":
		return formatted + r.cfg.Separator + base + r.cfg.Separator + formatted, true, nil
	default:
		return formatted + r.cfg.Separator + base, true, nil
	}
}

func (r *DateTimeRule) extractTime(f *types.FileInfo) (time.Time, error) {
	source := strings.ToLower(r.cfg.Source)
	switch source {
	case "auto":
		if f.ExifDate != nil && !f.ExifDate.IsZero() {
			return *f.ExifDate, nil
		}
		if t, ok := parseTimestampFromName(f.Name + f.Ext); ok {
			return t, nil
		}
		if !f.CreateTime.IsZero() {
			return f.CreateTime, nil
		}
		return f.ModTime, nil
	case "exif":
		if f.ExifDate != nil && !f.ExifDate.IsZero() {
			return *f.ExifDate, nil
		}
		return time.Time{}, fmt.Errorf("no exif data")
	case "filename", "name":
		if t, ok := parseTimestampFromName(f.Name + f.Ext); ok {
			return t, nil
		}
		return time.Time{}, fmt.Errorf("no timestamp in filename")
	case "mtime", "modified":
		return f.ModTime, nil
	case "ctime", "created", "birth":
		if !f.CreateTime.IsZero() {
			return f.CreateTime, nil
		}
		return f.ModTime, nil
	default:
		return f.ModTime, nil
	}
}

func (r *DateTimeRule) convertFormat(f string) string {
	return strings.NewReplacer(
		"YYYY", "2006",
		"yy", "06",
		"MM", "01",
		"DD", "02",
		"dd", "02",
		"HH", "15",
		"hh", "03",
		"mm", "04",
		"SS", "05",
		"ss", "05",
	).Replace(f)
}

var timestampPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(20\d{2})[-_]?(\d{2})[-_]?(\d{2})[T _-]?(\d{2})[-_:]?(\d{2})[-_:]?(\d{2})`),
	regexp.MustCompile(`(20\d{2})[-_]?(\d{2})[-_]?(\d{2})[T _-]?(\d{2})[-_:]?(\d{2})`),
	regexp.MustCompile(`(20\d{2})[-_]?(\d{2})[-_]?(\d{2})`),
	regexp.MustCompile(`(20\d{2})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`),
	regexp.MustCompile(`(20\d{2})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})`),
	regexp.MustCompile(`(20\d{2})(\d{2})(\d{2})`),
	regexp.MustCompile(`(19\d{2}|20\d{2})\.(\d{1,2})\.(\d{1,2})`),
	regexp.MustCompile(`IMG[_-]?(20\d{2})(\d{2})(\d{2})[_-]?(\d{2})?(\d{2})?(\d{2})?`),
	regexp.MustCompile(`VID[_-]?(20\d{2})(\d{2})(\d{2})[_-]?(\d{2})?(\d{2})?(\d{2})?`),
	regexp.MustCompile(`Screenshot[_\s-]*(20\d{2})[-_]?(\d{2})[-_]?(\d{2})`),
	regexp.MustCompile(`\b(\d{10,13})\b`),
}

func parseTimestampFromName(name string) (time.Time, bool) {
	for _, re := range timestampPatterns {
		matches := re.FindStringSubmatch(name)
		if matches == nil {
			continue
		}

		if len(matches) >= 2 && len(matches[1]) >= 10 {
			if ts, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
				if ts > 1e12 { ts /= 1000 }
				if ts > 946684800 && ts < 4102444800 {
					return time.Unix(ts, 0), true
				}
			}
		}

		year := getInt(matches, 1, 0)
		month := getInt(matches, 2, 1)
		day := getInt(matches, 3, 1)
		hour := getInt(matches, 4, 0)
		min := getInt(matches, 5, 0)
		sec := getInt(matches, 6, 0)

		if year >= 1990 && year <= 2100 && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			if hour > 23 { hour = 0 }
			if min > 59 { min = 0 }
			if sec > 59 { sec = 0 }
			t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local)
			return t, true
		}
	}
	return time.Time{}, false
}

func getInt(matches []string, idx, def int) int {
	if idx >= len(matches) {
		return def
	}
	if v, err := strconv.Atoi(matches[idx]); err == nil {
		return v
	}
	return def
}
