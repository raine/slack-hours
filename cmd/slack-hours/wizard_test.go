package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWizardValues(t *testing.T) {
	valid := wizardValues{Workspace: " https://app.slack.com/client/TWORK/channel ", Name: " Work ", Days: []string{"mon", "fri"}, Hours: "09:00-17:00", TimeZone: "Europe/London", Port: "9339"}
	c, err := valid.config()
	if err != nil || c.WorkspaceID != "TWORK" || c.WorkspaceName != "Work" || c.Schedule.Start != "09:00" || c.Port != 9339 {
		t.Fatal(c, err)
	}
	for _, mutate := range []func(*wizardValues){
		func(v *wizardValues) { v.Workspace = "https://example.com/client/TWORK" },
		func(v *wizardValues) { v.Name = " " },
		func(v *wizardValues) { v.Days = nil },
		func(v *wizardValues) { v.Hours = "17:00-09:00" },
		func(v *wizardValues) { v.TimeZone = "Bad/Zone" },
		func(v *wizardValues) { v.Port = "80" },
	} {
		v := valid
		mutate(&v)
		if _, err := v.config(); err == nil {
			t.Fatal("accepted invalid values", v)
		}
	}
}
func TestWizardCancelLeavesFilesUnchanged(t *testing.T) {
	for _, cancelErr := range []error{errSetupCancelled, context.Canceled} {
		p := Paths{t.TempDir(), t.TempDir()}
		if err := writeJSON(p.configFile(), testConfig()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.overrideFile(), []byte("original override"), 0600); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(p.configFile())
		err := configureWithWizard(context.Background(), p, nil, io.Discard, func(context.Context, io.Reader, io.Writer, Config) (Config, error) { return Config{}, cancelErr })
		if !errors.Is(err, cancelErr) {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(p.configFile())
		override, _ := os.ReadFile(p.overrideFile())
		if !bytes.Equal(before, after) || string(override) != "original override" {
			t.Fatal("cancellation changed files")
		}
	}
}
func TestWizardSavesPrivateConfig(t *testing.T) {
	p := Paths{filepath.Join(t.TempDir(), "config"), t.TempDir()}
	err := configureWithWizard(context.Background(), p, nil, io.Discard, func(context.Context, io.Reader, io.Writer, Config) (Config, error) { return testConfig(), nil })
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := p.load()
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkspaceID != "TWORK" || c.Schedule.TimeZone != "Europe/Helsinki" {
		t.Fatal(c)
	}
	info, err := os.Stat(p.configFile())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}

func TestWizardDoesNotOverwriteConcurrentEdit(t *testing.T) {
	p := Paths{t.TempDir(), t.TempDir()}
	c := testConfig()
	if err := writeJSON(p.configFile(), c); err != nil {
		t.Fatal(err)
	}
	err := configureWithWizard(context.Background(), p, nil, io.Discard, func(_ context.Context, _ io.Reader, _ io.Writer, draft Config) (Config, error) {
		c.WorkspaceName = "Changed elsewhere"
		if err := writeJSON(p.configFile(), c); err != nil {
			return draft, err
		}
		return draft, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed during setup") {
		t.Fatal(err)
	}
	actual, _, err := p.load()
	if err != nil || actual.WorkspaceName != c.WorkspaceName {
		t.Fatal(actual, err)
	}
}

func TestWizardCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := configurationWizard(ctx, strings.NewReader(""), io.Discard, defaultConfig())
	if !errors.Is(err, errSetupCancelled) {
		t.Fatal(err)
	}
}
