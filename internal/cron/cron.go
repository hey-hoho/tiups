// Package cron manages the user's crontab entry that runs `tiup-backup sync`.
package cron

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"tiup-backup/internal/shell"
)

// Marker tags the crontab line owned by this tool, so set/disable never touch other entries.
const Marker = "# tiup-backup:sync"

var (
	cronField  = regexp.MustCompile(`^[0-9A-Za-z*/,\-]+$`)
	cronMacros = map[string]bool{
		"@hourly": true, "@daily": true, "@midnight": true, "@weekly": true,
		"@monthly": true, "@yearly": true, "@annually": true,
	}
)

// Normalize validates a 5-field cron expression or an @macro.
func Normalize(s string) (string, error) {
	f := strings.Fields(s)
	if len(f) == 1 && strings.HasPrefix(f[0], "@") {
		if !cronMacros[f[0]] {
			return "", fmt.Errorf("unsupported cron macro %q", f[0])
		}
		return f[0], nil
	}
	if len(f) != 5 {
		return "", fmt.Errorf("cron expression needs 5 fields (min hour day month weekday), got %d: %q", len(f), s)
	}
	for i, x := range f {
		if !cronField.MatchString(x) {
			return "", fmt.Errorf("invalid cron field %q", x)
		}
		if err := checkField(x, fieldSpecs[i]); err != nil {
			return "", err
		}
	}
	return strings.Join(f, " "), nil
}

type fieldSpec struct {
	name   string
	lo, hi int
	names  []string // 3-letter names, value = index + nameBase
	nameLo int
}

var fieldSpecs = [5]fieldSpec{
	{name: "minute", lo: 0, hi: 59},
	{name: "hour", lo: 0, hi: 23},
	{name: "day of month", lo: 1, hi: 31},
	{name: "month", lo: 1, hi: 12, nameLo: 1,
		names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{name: "weekday", lo: 0, hi: 7, nameLo: 0,
		names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}},
}

// checkField validates one field such as "*/5", "1-3,7" or "mon-fri" against its numeric range.
func checkField(x string, sp fieldSpec) error {
	bad := func() error { return fmt.Errorf("invalid %s field %q (allowed %d-%d)", sp.name, x, sp.lo, sp.hi) }
	value := func(s string) (int, bool) {
		if n, err := strconv.Atoi(s); err == nil {
			return n, n >= sp.lo && n <= sp.hi
		}
		for i, nm := range sp.names {
			if strings.EqualFold(s, nm) {
				return i + sp.nameLo, true
			}
		}
		return 0, false
	}
	for _, part := range strings.Split(x, ",") {
		base, step, hasStep := strings.Cut(part, "/")
		if hasStep {
			if n, err := strconv.Atoi(step); err != nil || n < 1 {
				return bad()
			}
		}
		if base == "*" {
			continue
		}
		lo, hi, isRange := strings.Cut(base, "-")
		a, ok := value(lo)
		if !ok {
			return bad()
		}
		if isRange {
			b, ok := value(hi)
			if !ok || b < a {
				return bad()
			}
		}
	}
	return nil
}

// BuildLine renders the crontab line that runs `exe sync` with TIUP_HOME=home, logging to logPath.
func BuildLine(schedule, home, exe, logPath string) (string, error) {
	for _, p := range []string{home, exe, logPath} {
		// '%' is special in crontab and cannot be escaped reliably inside quotes.
		if strings.ContainsAny(p, "%\n") {
			return "", fmt.Errorf("path %q contains a character that crontab cannot represent", p)
		}
	}
	return fmt.Sprintf("%s TIUP_HOME=%s %s sync >> %s 2>&1 %s",
		schedule, shell.Quote(home), shell.Quote(exe), shell.Quote(logPath), Marker), nil
}

// ParseSchedule returns the schedule part of a job line.
func ParseSchedule(line string) string {
	f := strings.Fields(line)
	if len(f) > 0 && strings.HasPrefix(f[0], "@") {
		return f[0]
	}
	if len(f) >= 5 {
		return strings.Join(f[:5], " ")
	}
	return ""
}

// Replace drops existing job lines and appends newLine (when non-empty).
func Replace(lines []string, newLine string) []string {
	var out []string
	for _, l := range lines {
		if !strings.Contains(l, Marker) {
			out = append(out, l)
		}
	}
	if newLine != "" {
		out = append(out, newLine)
	}
	return out
}

// Find returns our job line, if present.
func Find(lines []string) (string, bool) {
	for _, l := range lines {
		if strings.Contains(l, Marker) {
			return l, true
		}
	}
	return "", false
}

// Require fails on platforms without cron.
func Require() error {
	if runtime.GOOS == "windows" {
		return errors.New("scheduled jobs rely on cron and are only supported on Linux/macOS")
	}
	return nil
}

// ReadCrontab returns the current user's crontab lines (none if it is empty).
func ReadCrontab() ([]string, error) {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("crontab command not found (install cronie / cron and start the service)")
		}
		if strings.Contains(string(out), "no crontab") {
			return nil, nil
		}
		return nil, fmt.Errorf("crontab -l: %v: %s", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// WriteCrontab replaces the current user's crontab; no lines removes it.
func WriteCrontab(lines []string) error {
	if len(lines) == 0 {
		if out, err := exec.Command("crontab", "-r").CombinedOutput(); err != nil && !strings.Contains(string(out), "no crontab") {
			return fmt.Errorf("crontab -r: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Tail returns the last n non-empty lines of a file (nil if unreadable).
func Tail(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
