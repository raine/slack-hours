package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func TestScheduleBoundaries(t *testing.T) {
	c := testConfig()
	loc, _ := time.LoadLocation(c.Schedule.TimeZone)
	for _, tc := range []struct {
		at     string
		hidden bool
	}{
		{"2026-10-27T03:59:59Z", true}, {"2026-10-27T04:00:00Z", false}, {"2026-10-27T15:59:59Z", false}, {"2026-10-27T16:00:00Z", true},
		{"2026-10-31T10:00:00Z", true}, {"2026-11-01T10:00:00Z", true}, {"2026-11-02T04:00:00Z", false}, {"2026-07-06T03:00:00Z", false},
	} {
		t.Run(tc.at, func(t *testing.T) {
			if got := c.Schedule.hidden(instant(tc.at), loc); got != tc.hidden {
				t.Fatal(got)
			}
		})
	}
}
func TestTransitionsAndOverrides(t *testing.T) {
	c := testConfig()
	loc, _ := time.LoadLocation(c.Schedule.TimeZone)
	for _, tc := range []struct{ at, want string }{
		{"2026-10-23T15:00:00Z", "2026-10-26T04:00:00Z"}, {"2026-03-27T16:00:00Z", "2026-03-30T03:00:00Z"},
	} {
		if got := c.Schedule.nextTransition(instant(tc.at), loc); !got.Equal(instant(tc.want)) {
			t.Fatal(got)
		}
	}
	now := instant("2026-10-27T17:30:00Z")
	expiry := c.Schedule.nextTransition(now, loc)
	o := Override{"show", expiry}
	if got := desired(now, c, o); got.Hidden || got.Mode != "show" {
		t.Fatal(got)
	}
	if got := desired(expiry, c, o); got.Hidden || got.Mode != "auto" {
		t.Fatal(got)
	}
	c.Schedule.Start, c.Schedule.End = "06:15", "06:16"
	if got := c.Schedule.nextTransition(instant("2026-10-27T04:00:00Z"), loc); !got.Equal(instant("2026-10-27T04:15:00Z")) {
		t.Fatal(got)
	}
}
func TestValidationAndScheduleFlags(t *testing.T) {
	c := testConfig()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--hours", "18:00-06:00"}, {"--hours", "6:00-18:00"}, {"--days", "bogus"}, {"--days", "mon,mon"}, {"--timezone", "bogus"}, {"--unknown", "x"}, {"--days"}, {"--hours", "06:00-18:00-extra"}, {"--days", "mon", "--days", "tue"}} {
		if _, err := parseSchedule(args, c.Schedule); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	s, err := parseSchedule([]string{"--days", "mon,tue", "--hours", "08:30-17:15"}, c.Schedule)
	if err != nil || s.Start != "08:30" || !reflect.DeepEqual(s.Days, []string{"mon", "tue"}) {
		t.Fatal(s, err)
	}
	c.WorkspaceID = `bad"selector`
	if c.validate() == nil {
		t.Fatal("accepted bad ID")
	}
}
func TestExistingJSONAndAtomicWrite(t *testing.T) {
	p := Paths{filepath.Join(t.TempDir(), "config"), t.TempDir()}
	if err := writeJSON(p.configFile(), testConfig()); err != nil {
		t.Fatal(err)
	}
	o := Override{"show", instant("2026-10-28T04:00:00Z")}
	if err := writeJSON(p.overrideFile(), o); err != nil {
		t.Fatal(err)
	}
	c, got, err := p.load()
	if err != nil || !reflect.DeepEqual(c, testConfig()) || got != o {
		t.Fatal(c, got, err)
	}
	info, _ := os.Stat(p.configFile())
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	files, _ := filepath.Glob(filepath.Join(p.ConfigDir, ".slack-hours-*"))
	if len(files) > 0 {
		t.Fatal(files)
	}
}
func TestFlockCancellationAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := lockFile(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockFile(context.Background(), path, false); err == nil {
		t.Fatal("second owner acquired lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := lockFile(ctx, path, true); err == nil {
		t.Fatal("lock wait should time out")
	}
	unlock()
	unlock, err = lockFile(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
func BenchmarkNextTransition(b *testing.B) {
	c := testConfig()
	loc, _ := time.LoadLocation(c.Schedule.TimeZone)
	now := instant("2026-10-23T15:00:00Z")
	for b.Loop() {
		c.Schedule.nextTransition(now, loc)
	}
}

func testConfig() Config {
	c := defaultConfig()
	c.WorkspaceID, c.WorkspaceName = "TWORK", "Work"
	return c
}

func TestInitRequiresIdentityAndPreservesConfig(t *testing.T) {
	p := Paths{filepath.Join(t.TempDir(), "config"), t.TempDir()}
	if runCLI(context.Background(), []string{"init"}, p) == 0 {
		t.Fatal("accepted missing identity")
	}
	if _, err := os.Stat(p.configFile()); !os.IsNotExist(err) {
		t.Fatal("wrote invalid config")
	}
	if runCLI(context.Background(), []string{"init", "--workspace-id", "TWORK", "--workspace-name", "Work"}, p) != 0 {
		t.Fatal("init failed")
	}
	before, _ := os.ReadFile(p.configFile())
	if runCLI(context.Background(), []string{"init"}, p) != 0 {
		t.Fatal("idempotent init failed")
	}
	after, _ := os.ReadFile(p.configFile())
	if string(before) != string(after) {
		t.Fatal("overwrote config")
	}
}
