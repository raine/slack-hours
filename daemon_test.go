package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEngine struct {
	calls   atomic.Int32
	changes chan struct{}
	do      func(int32) (Result, error)
}

func (f *fakeEngine) Do(context.Context, Request) (Result, error) {
	n := f.calls.Add(1)
	if f.do != nil {
		return f.do(n)
	}
	return Result{Healthy: true, Target: Target{Until: time.Now().Add(time.Hour)}}, nil
}
func (f *fakeEngine) Changes() <-chan struct{}      { return f.changes }
func (f *fakeEngine) Disconnected() <-chan struct{} { return nil }
func (f *fakeEngine) Close()                        {}
func waitCalls(t *testing.T, f *fakeEngine, n int32) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for f.calls.Load() < n && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.calls.Load() < n {
		t.Fatalf("got %d calls; want %d", f.calls.Load(), n)
	}
}
func startLoop(t *testing.T, f *fakeEngine, health time.Duration) (chan work, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	requests := make(chan work, 4)
	done := make(chan struct{})
	go func() { defer close(done); _ = runLoop(ctx, f, requests, health, io.Discard, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("loop did not stop")
		}
	})
	return requests, cancel
}
func TestDaemonSleepsUntilEventOrRequest(t *testing.T) {
	f := &fakeEngine{changes: make(chan struct{}, 1)}
	requests, _ := startLoop(t, f, time.Hour)
	waitCalls(t, f, 1)
	time.Sleep(50 * time.Millisecond)
	if f.calls.Load() != 1 {
		t.Fatal("unexpected idle reconciliation")
	}
	f.changes <- struct{}{}
	waitCalls(t, f, 2)
	job := work{Request{Command: "status"}, make(chan Response, 1)}
	requests <- job
	select {
	case r := <-job.reply:
		if !r.Result.Healthy {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("request blocked")
	}
	if f.calls.Load() != 3 {
		t.Fatal(f.calls.Load())
	}
}
func TestScheduleTimerAndHealthCheck(t *testing.T) {
	f := &fakeEngine{do: func(n int32) (Result, error) {
		until := time.Now().Add(time.Hour)
		if n == 1 {
			until = time.Now().Add(20 * time.Millisecond)
		}
		return Result{Healthy: true, Target: Target{Until: until}}, nil
	}}
	_, cancel := startLoop(t, f, time.Hour)
	waitCalls(t, f, 2)
	cancel()
	f2 := &fakeEngine{}
	startLoop(t, f2, 20*time.Millisecond)
	waitCalls(t, f2, 2)
}
func TestFailureBackoffDoesNotSpin(t *testing.T) {
	f := &fakeEngine{do: func(int32) (Result, error) {
		return Result{Target: Target{Until: time.Now().Add(-time.Hour)}}, errors.New("offline")
	}}
	startLoop(t, f, time.Hour)
	waitCalls(t, f, 1)
	time.Sleep(80 * time.Millisecond)
	if f.calls.Load() != 1 {
		t.Fatal("retry loop is spinning")
	}
}
func TestUnixProtocolAndMissingDaemon(t *testing.T) {
	// macOS Unix socket paths must fit sockaddr_un.sun_path.
	dir, err := os.MkdirTemp("/tmp", "sw-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	p := Paths{StateDir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, available, err := remoteRequest(ctx, p, Request{Command: "status"}); available || err != nil {
		t.Fatal(available, err)
	}
	listener, err := net.Listen("unix", p.socketFile())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan work, 1)
	go serveRequests(ctx, listener, requests)
	go func() {
		job := <-requests
		job.reply <- Response{Result: Result{Healthy: job.request.Command == "status"}}
	}()
	response, available, err := remoteRequest(ctx, p, Request{Command: "status"})
	if err != nil || !available || !response.Result.Healthy {
		t.Fatal(response, available, err)
	}
}
func TestOfflineCommandsSaveDesiredStateAndValidateBeforeWrite(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	p := Paths{ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	cfg := testConfig()
	cfg.Port = port
	if err := writeJSON(p.configFile(), cfg); err != nil {
		t.Fatal(err)
	}
	controller := &Controller{Paths: p}
	defer controller.Close()
	if _, err := controller.Do(context.Background(), Request{Command: "hide"}); err == nil {
		t.Fatal("expected offline error")
	}
	_, override, err := p.load()
	if err != nil || override.Mode != "hide" || override.ExpiresAt.Before(time.Now()) {
		t.Fatal(override, err)
	}
	before, _ := os.ReadFile(p.configFile())
	s := cfg.Schedule
	s.Start = "25:00"
	if _, err = controller.Do(context.Background(), Request{Command: "schedule", Schedule: &s}); err == nil {
		t.Fatal("accepted invalid config")
	}
	after, _ := os.ReadFile(p.configFile())
	if string(before) != string(after) {
		t.Fatal("invalid config persisted")
	}
	_, _ = controller.Do(context.Background(), Request{Command: "auto"})
	if _, err = os.Stat(p.overrideFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestLaunchAgentHasNoNodeDependency(t *testing.T) {
	p := Paths{"/a&b/config", "/state"}
	text := launchAgentXML("/a b/slack-hours", p)
	for _, want := range []string{"/a b/slack-hours", "/a&amp;b/config", "<string>daemon</string>"} {
		if !strings.Contains(text, want) {
			t.Fatal(want)
		}
	}
	if strings.Contains(text, "node") || strings.Contains(text, ".mjs") {
		t.Fatal("Node runtime retained")
	}
}

func TestStatusDoesNotPostponeScheduleTimer(t *testing.T) {
	f := &fakeEngine{do: func(n int32) (Result, error) {
		until := time.Now().Add(time.Hour)
		if n == 1 {
			until = time.Now().Add(60 * time.Millisecond)
		}
		return Result{Healthy: true, Target: Target{Until: until}}, nil
	}}
	requests, _ := startLoop(t, f, time.Hour)
	waitCalls(t, f, 1)
	job := work{Request{Command: "status"}, make(chan Response, 1)}
	requests <- job
	<-job.reply
	waitCalls(t, f, 3)
}
