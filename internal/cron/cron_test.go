package cron

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"0 2 * * *":               "0 2 * * *",
		"  */5  *  * * * ":        "*/5 * * * *",
		"0 3 * * mon-fri":         "0 3 * * mon-fri",
		"@daily":                  "@daily",
		"0,30 8-18 1 jan,jul sun": "0,30 8-18 1 jan,jul sun",
		"*/15 */2 * * 1-5":        "*/15 */2 * * 1-5",
		"0 0 * * 7":               "0 0 * * 7",
	}
	for in, want := range ok {
		if got, err := Normalize(in); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0 2 * *", "0 2 * * * *", "@reboot", "0 2 * * $(x)", "a;b c d e", "0 2 * * %",
		"61 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"5-1 * * * *", "*/0 * * * *", "*/x * * * *", "* * * foo *", "1- * * * *"} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) should fail", in)
		}
	}
}

func TestJobLine(t *testing.T) {
	const log = "/root/.tiup/storage/meta/sync.log"
	line, err := BuildLine("0 2 * * *", "/root/.tiup", "/root/.tiup/bin/tiup-backup", log)
	if err != nil {
		t.Fatal(err)
	}
	want := "0 2 * * * TIUP_HOME='/root/.tiup' '/root/.tiup/bin/tiup-backup' sync >> '" + log + "' 2>&1 " + Marker
	if line != want {
		t.Fatalf("got  %s\nwant %s", line, want)
	}
	if got := ParseSchedule(line); got != "0 2 * * *" {
		t.Errorf("parseSchedule = %q", got)
	}
	if got := ParseSchedule("@daily TIUP_HOME=x y " + Marker); got != "@daily" {
		t.Errorf("parseSchedule macro = %q", got)
	}
	if _, err := BuildLine("@daily", "/a%b", "/x", log); err == nil {
		t.Error("'%' in path must be rejected")
	}
	if l, _ := BuildLine("@daily", "/it's", "/x", log); !strings.Contains(l, `'/it'\''s'`) {
		t.Errorf("single quote not escaped: %s", l)
	}
}

func TestReplaceJob(t *testing.T) {
	other := []string{"# mine", "*/10 * * * * /usr/bin/foo"}
	old := "0 1 * * * x " + Marker

	got := Replace(append(append([]string{}, other...), old), "0 2 * * * y "+Marker)
	if len(got) != 3 || got[0] != other[0] || got[1] != other[1] || !strings.HasPrefix(got[2], "0 2") {
		t.Fatalf("replace: %v", got)
	}
	got = Replace(got, "")
	if len(got) != 2 {
		t.Fatalf("disable should only drop our line: %v", got)
	}
	if _, ok := Find(got); ok {
		t.Error("job still present")
	}
}
