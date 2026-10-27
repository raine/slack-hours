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

func TestWizardValidationAndURL(t *testing.T) {
	input := "invalid\nhttps://app.slack.com/client/TWORK/channel\nWork\nmon,mon\nmon-fri\n17:00-09:00\n09:00-17:00\nBad/Zone\nEurope/London\n80\n9339\ny\n"
	var out bytes.Buffer
	c, err := configurationWizard(context.Background(), strings.NewReader(input), &out, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkspaceID != "TWORK" || c.WorkspaceName != "Work" || c.Schedule.Start != "09:00" || c.Schedule.End != "17:00" || c.Schedule.TimeZone != "Europe/London" || c.Port != 9339 {
		t.Fatal(c)
	}
	for _, message := range []string{"enter a workspace ID", "invalid schedule days", "same-day interval", "enter a port", "Workspace: Work (TWORK)"} {
		if !strings.Contains(out.String(), message) {
			t.Fatalf("missing %q in %s", message, out.String())
		}
	}
}
func TestWizardCancelLeavesFilesUnchanged(t *testing.T) {
	for _, answer := range []string{"", "\n\n\n\n\n\nn\n"} {
		p := Paths{t.TempDir(), t.TempDir()}
		if err := writeJSON(p.configFile(), testConfig()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.overrideFile(), []byte("original override"), 0600); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(p.configFile())
		err := configure(context.Background(), p, strings.NewReader(answer), io.Discard)
		if !errors.Is(err, errSetupCancelled) {
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
	err := configure(context.Background(), p, strings.NewReader("TWORK\nWork\n\n\n\n\ny\n"), io.Discard)
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

type wizardWriter func([]byte) (int, error)

func (w wizardWriter) Write(b []byte) (int, error) { return w(b) }

func TestWizardDoesNotOverwriteConcurrentEdit(t *testing.T) {
	p := Paths{t.TempDir(), t.TempDir()}
	c := testConfig()
	if err := writeJSON(p.configFile(), c); err != nil {
		t.Fatal(err)
	}
	out := wizardWriter(func(b []byte) (int, error) {
		if strings.Contains(string(b), "Save configuration?") {
			c.WorkspaceName = "Changed elsewhere"
			if err := writeJSON(p.configFile(), c); err != nil {
				return 0, err
			}
		}
		return len(b), nil
	})
	err := configure(context.Background(), p, strings.NewReader("\n\n\n\n\n\ny\n"), out)
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
