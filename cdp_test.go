package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func fakeCDP(t *testing.T, handle func(*websocket.Conn)) *CDP {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newCDP(conn, nil)
	t.Cleanup(c.Close)
	return c
}
func TestCDPConcurrentCalls(t *testing.T) {
	c := fakeCDP(t, func(conn *websocket.Conn) {
		for {
			var m wireMessage
			if conn.ReadJSON(&m) != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{"id": m.ID, "result": map[string]string{"method": m.Method}})
		}
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got map[string]string
			if err := c.Call(context.Background(), "Test", nil, &got); err != nil || got["method"] != "Test" {
				t.Error(got, err)
			}
		}()
	}
	wg.Wait()
	if c.Calls.Load() != 20 {
		t.Fatal(c.Calls.Load())
	}
}
func TestCDPCancelAndDisconnect(t *testing.T) {
	c := fakeCDP(t, func(conn *websocket.Conn) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "Slow", nil, nil); err == nil {
		t.Fatal("expected timeout")
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatal("leaked pending call")
	}
	c.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := c.Call(ctx2, "Gone", nil, nil); err == nil {
		t.Fatal("expected disconnect")
	}
}
func TestEvaluationDoesNotExposeAppData(t *testing.T) {
	var value bool
	_, err := decodeEvaluation(evaluated{Exception: json.RawMessage(`{"text":"SECRET"}`)}, &value)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
}
func TestRuntimeEventsTrackRootContextsAndCoalesce(t *testing.T) {
	s := &Slack{Changed: make(chan struct{}, 1), paused: make(chan pause, 1), contexts: map[int]executionContext{}, frame: "root"}
	s.event("Runtime.executionContextCreated", json.RawMessage(`{"context":{"id":1,"name":"","auxData":{"isDefault":true,"frameId":"root"}}}`))
	s.event("Runtime.executionContextCreated", json.RawMessage(`{"context":{"id":2,"name":"Electron Isolated Context","auxData":{"frameId":"root"}}}`))
	s.event("Runtime.executionContextCreated", json.RawMessage(`{"context":{"id":3,"name":"","auxData":{"isDefault":true,"frameId":"iframe"}}}`))
	main, isolated, err := s.worlds()
	if err != nil || main != 1 || isolated != 2 {
		t.Fatal(main, isolated, err)
	}
	if len(s.Changed) != 1 {
		t.Fatal("events not coalesced")
	}
	s.event("Runtime.executionContextsCleared", json.RawMessage(`{}`))
	if _, _, err = s.worlds(); err == nil {
		t.Fatal("old contexts retained")
	}
}
func TestGuardStatusRequiresEventDrivenUI(t *testing.T) {
	visible := false
	a := Actual{Connected: true, WorkspaceID: "EWORK", Native: &NativeStatus{Installed: true, WorkspaceID: "EWORK", Hidden: true, WorkspaceKnown: true, NotificationHookIntact: true, WorkspaceHookIntact: true}, Web: &WebStatus{Installed: true, WorkspaceID: "EWORK", Hidden: true, SoundPatched: true, EventDriven: true, ShortcutGuard: true, SidebarFound: true, SidebarVisible: &visible, ActiveWorkspace: "TPERSONAL"}}
	if !a.applied(true) {
		t.Fatal(a)
	}
	a.Web.ShortcutGuard = false
	if a.applied(true) {
		t.Fatal("accepted missing shortcut guard")
	}
	a.Web.ShortcutGuard = true
	a.Web.EventDriven = false
	if a.applied(true) {
		t.Fatal("accepted old polling hook")
	}
	a.Web.EventDriven = true
	a.Web.ActiveWorkspace = "EWORK"
	if a.applied(true) {
		t.Fatal("work still active")
	}
}
