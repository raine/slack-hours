package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

type Schedule struct {
	Days     []string `json:"days"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	TimeZone string   `json:"timeZone"`
}
type Config struct {
	WorkspaceID   string   `json:"workspaceId"`
	WorkspaceName string   `json:"workspaceName"`
	Port          int      `json:"port"`
	Schedule      Schedule `json:"schedule"`
}
type Override struct {
	Mode      string    `json:"mode"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type Target struct {
	Hidden bool      `json:"hidden"`
	Mode   string    `json:"mode"`
	Until  time.Time `json:"until"`
}
type Paths struct{ ConfigDir, StateDir string }

func defaultPaths() Paths {
	home, _ := os.UserHomeDir()
	config, state := os.Getenv("SLACK_HOURS_CONFIG_DIR"), os.Getenv("SLACK_HOURS_STATE_DIR")
	if config == "" {
		config = filepath.Join(home, ".config/slack-hours")
	}
	if state == "" {
		state = filepath.Join(home, ".local/state/slack-hours")
	}
	return Paths{config, state}
}
func (p Paths) configFile() string   { return filepath.Join(p.ConfigDir, "config.json") }
func (p Paths) overrideFile() string { return filepath.Join(p.StateDir, "override.json") }
func (p Paths) socketFile() string   { return filepath.Join(p.StateDir, "control.sock") }
func defaultConfig() Config {
	return Config{"", "", 9338, Schedule{[]string{"mon", "tue", "wed", "thu", "fri"}, "09:00", "17:00", defaultTimeZone()}}
}

func defaultTimeZone() string {
	if zone := os.Getenv("TZ"); zone != "" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	if path, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(path, "zoneinfo/"); ok {
			if _, err := time.LoadLocation(zone); err == nil {
				return zone
			}
		}
	}
	return "UTC"
}

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
var validTime = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
var validID = regexp.MustCompile(`^[ET][A-Z0-9]+$`)

func (c Config) validate() error {
	if !validID.MatchString(c.WorkspaceID) || strings.TrimSpace(c.WorkspaceName) == "" {
		return errors.New("invalid workspace identity")
	}
	if c.Port < 1024 || c.Port > 65535 {
		return errors.New("invalid debug port")
	}
	return c.Schedule.validate()
}
func (s Schedule) validate() error {
	seen := map[string]bool{}
	for _, day := range s.Days {
		valid := false
		for _, d := range weekdays {
			if d == day {
				valid = true
			}
		}
		if !valid || seen[day] {
			return errors.New("invalid schedule days")
		}
		seen[day] = true
	}
	if len(s.Days) == 0 || !validTime.MatchString(s.Start) || !validTime.MatchString(s.End) || s.Start >= s.End {
		return errors.New("hours must be a same-day interval, e.g. 06:00-18:00")
	}
	if s.TimeZone == "" {
		return errors.New("missing timezone")
	}
	_, err := time.LoadLocation(s.TimeZone)
	return err
}
func (s Schedule) hidden(t time.Time, loc *time.Location) bool {
	local := t.In(loc)
	clock := local.Format("15:04")
	for _, day := range s.Days {
		if day == weekdays[local.Weekday()] {
			return clock < s.Start || clock >= s.End
		}
	}
	return true
}
func (s Schedule) nextTransition(now time.Time, loc *time.Location) time.Time {
	hidden := s.hidden(now, loc)
	// Scan real minutes so skipped and repeated local times honor DST.
	for t, end := now.Truncate(time.Minute).Add(time.Minute), now.Add(8*24*time.Hour); t.Before(end); t = t.Add(time.Minute) {
		if s.hidden(t, loc) != hidden {
			return t
		}
	}
	return now.Add(24 * time.Hour)
}
func desired(now time.Time, c Config, override Override) Target {
	loc, _ := time.LoadLocation(c.Schedule.TimeZone)
	if (override.Mode == "show" || override.Mode == "hide") && override.ExpiresAt.After(now) {
		return Target{override.Mode == "hide", override.Mode, override.ExpiresAt}
	}
	return Target{c.Schedule.hidden(now, loc), "auto", c.Schedule.nextTransition(now, loc)}
}
func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0600)
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".slack-hours-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (p Paths) load() (Config, Override, error) {
	var c Config
	var o Override
	if err := readJSON(p.configFile(), &c); err != nil {
		return c, o, fmt.Errorf("load config (run slack-hours init first): %w", err)
	}
	if err := c.validate(); err != nil {
		return c, o, err
	}
	if err := readJSON(p.overrideFile(), &o); err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, o, err
	}
	return c, o, nil
}
func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// flock ownership is released by the kernel even when a command crashes.
func lockFile(ctx context.Context, path string, wait bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !wait || (!errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN)) {
			f.Close()
			return nil, fmt.Errorf("another slack-hours process holds the lock: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
