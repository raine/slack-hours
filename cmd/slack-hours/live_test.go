package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Opt-in checks exercise the installed service and the actual Slack renderer.
// They send only synthetic local notifications, never workspace messages.
func TestLiveDaemon(t *testing.T) {
	if os.Getenv("SLACK_HOURS_LIVE") != "1" {
		t.Skip("set SLACK_HOURS_LIVE=1 to exercise the installed Slack app")
	}
	p := defaultPaths()
	config, _, err := p.load()
	if err != nil {
		t.Fatal(err)
	}
	original, readErr := os.ReadFile(p.overrideFile())
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if readErr == nil {
			_ = atomicWrite(p.overrideFile(), original, 0600)
		} else {
			_ = removeFile(p.overrideFile())
		}
		if _, err := dispatch(cleanup, p, Request{Command: "reconcile"}); err != nil {
			t.Error("restore original mode:", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, cmd := range []string{"show", "hide", "check"} {
		result, err := dispatch(ctx, p, Request{Command: cmd})
		if err != nil || !result.Healthy {
			t.Fatal(cmd, result, err)
		}
		if cmd == "show" && result.Target.Hidden {
			t.Fatal("show did not restore workspace")
		}
	}
	if _, err = dispatch(ctx, p, Request{Command: "show"}); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(p.overrideFile(), Override{"show", time.Now().Add(400 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	before, err := dispatch(ctx, p, Request{Command: "reconcile"})
	if err != nil || before.Target.Mode != "show" {
		t.Fatal(before, err)
	}
	time.Sleep(600 * time.Millisecond)
	after, err := dispatch(ctx, p, Request{Command: "status"})
	if err != nil || !after.Healthy || after.Target.Mode != "auto" {
		t.Fatal("override timer failed", after, err)
	}
	if _, err = dispatch(ctx, p, Request{Command: "hide"}); err != nil {
		t.Fatal(err)
	}
	before, err = dispatch(ctx, p, Request{Command: "status"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := connectSlack(ctx, config.Port)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = session.cdp.Call(ctx, "Page.reload", nil, nil)
	session.Close()
	if err != nil {
		t.Fatal(err)
	}
	recovered := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		after, err = dispatch(ctx, p, Request{Command: "status"})
		if err == nil && after.Healthy && after.Actual.Web != nil && after.Actual.Web.InstanceID != "" && after.Actual.Web.InstanceID != before.Actual.Web.InstanceID {
			recovered = true
			break
		}
	}
	if !recovered {
		t.Fatal("reload recovery failed", after, err)
	}
	if after.Stats.Connections != before.Stats.Connections {
		t.Fatal("renderer reload replaced the persistent connection")
	}
	t.Logf("reinstalled guards after reload in %s on the same connection", time.Since(start).Round(time.Millisecond))
	if _, err = dispatch(ctx, p, Request{Command: "check"}); err != nil {
		t.Fatal(err)
	}
}

// Workspace discovery reads presentation data without changing visibility.
func TestLiveWorkspaceDiscovery(t *testing.T) {
	if os.Getenv("SLACK_HOURS_LIVE") != "1" {
		t.Skip("set SLACK_HOURS_LIVE=1 to read workspaces from Slack")
	}
	c, _, err := defaultPaths().load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workspaces, err := listWizardWorkspaces(ctx, c.Port)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range workspaces {
		if workspace.ID == c.WorkspaceID {
			if workspace.Name != c.WorkspaceName {
				t.Fatal("configured workspace name differs from Slack")
			}
			t.Logf("discovered %d workspaces, including the configured workspace", len(workspaces))
			return
		}
	}
	t.Fatal("configured workspace is missing from discovery")
}
