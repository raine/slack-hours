package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed hooks/native.js
var nativeHook string

//go:embed hooks/web.js
var webHook string

//go:embed hooks/sound.js
var soundHook string

//go:embed hooks/readiness.js
var readinessHook string

type executionContext struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Aux  struct {
		Default bool   `json:"isDefault"`
		FrameID string `json:"frameId"`
	} `json:"auxData"`
}
type pause struct {
	Frames []struct {
		Name string `json:"functionName"`
		ID   string `json:"callFrameId"`
	} `json:"callFrames"`
}
type nativeRestore struct {
	WorkspaceID string          `json:"workspaceId"`
	Workspace   json.RawMessage `json:"workspace"`
	Unreads     json.RawMessage `json:"unreads"`
}

type Slack struct {
	restore  *nativeRestore
	cdp      *CDP
	Port     int
	Version  string
	Changed  chan struct{}
	paused   chan pause
	mu       sync.Mutex
	contexts map[int]executionContext
	frame    string
}

func (s *Slack) signal() {
	select {
	case s.Changed <- struct{}{}:
	default:
	}
}
func (s *Slack) event(method string, params json.RawMessage) {
	switch method {
	case "Runtime.executionContextCreated":
		var p struct {
			Context executionContext `json:"context"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.mu.Lock()
		s.contexts[p.Context.ID] = p.Context
		relevant := p.Context.Aux.FrameID == s.frame && (p.Context.Aux.Default || p.Context.Name == "Electron Isolated Context")
		s.mu.Unlock()
		if relevant {
			s.signal()
		}
	case "Runtime.executionContextsCleared":
		s.mu.Lock()
		s.contexts = map[int]executionContext{}
		s.mu.Unlock()
		s.signal()
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID int `json:"executionContextId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.mu.Lock()
		previous := s.contexts[p.ID]
		relevant := previous.Aux.FrameID == s.frame && (previous.Aux.Default || previous.Name == "Electron Isolated Context")
		delete(s.contexts, p.ID)
		s.mu.Unlock()
		if relevant {
			s.signal()
		}
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ID     string `json:"id"`
				Parent string `json:"parentId"`
			} `json:"frame"`
		}
		if json.Unmarshal(params, &p) == nil && p.Frame.Parent == "" {
			s.mu.Lock()
			s.frame = p.Frame.ID
			s.mu.Unlock()
			s.signal()
		}
	case "Page.loadEventFired":
		s.signal()
	case "Runtime.bindingCalled":
		var p struct {
			Name      string `json:"name"`
			ContextID int    `json:"executionContextId"`
		}
		if json.Unmarshal(params, &p) == nil && p.Name == "__slackWorkReadySignal" {
			s.mu.Lock()
			world := s.contexts[p.ContextID]
			relevant := world.Aux.FrameID == s.frame && world.Aux.Default
			s.mu.Unlock()
			if relevant {
				s.signal()
			}
		}
	case "Debugger.paused":
		var p pause
		if json.Unmarshal(params, &p) == nil {
			select {
			case s.paused <- p:
			default:
			}
		}
	}
}
func connectSlack(ctx context.Context, port int) (*Slack, error) {
	endpoint, version, err := discovery(ctx, port)
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, Proxy: nil}
	conn, resp, err := dialer.DialContext(ctx, endpoint, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, errors.New("Slack debugger connection failed")
	}
	s := &Slack{Port: port, Version: version, Changed: make(chan struct{}, 1), paused: make(chan pause, 1), contexts: map[int]executionContext{}}
	s.cdp = newCDP(conn, s.event)
	fail := func(err error) (*Slack, error) { s.Close(); return nil, err }
	if err = s.cdp.Call(ctx, "Page.enable", nil, nil); err != nil {
		return fail(err)
	}
	var tree struct {
		Tree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err = s.cdp.Call(ctx, "Page.getFrameTree", nil, &tree); err != nil {
		return fail(err)
	}
	s.mu.Lock()
	s.frame = tree.Tree.Frame.ID
	s.mu.Unlock()
	if err = s.cdp.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		return fail(err)
	}
	if err = s.cdp.Call(ctx, "Runtime.addBinding", map[string]any{"name": "__slackWorkReadySignal"}, nil); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Slack) Close() { s.cdp.Close() }
func (s *Slack) worlds() (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	main, isolated := 0, 0
	for _, c := range s.contexts {
		if c.Aux.FrameID != s.frame {
			continue
		}
		if c.Aux.Default {
			main = c.ID
		}
		if c.Name == "Electron Isolated Context" {
			isolated = c.ID
		}
	}
	if main == 0 || isolated == 0 {
		return 0, 0, errors.New("Slack execution contexts are not ready")
	}
	return main, isolated, nil
}

type NativeStatus struct {
	Installed              bool   `json:"installed"`
	WorkspaceID            string `json:"workspaceId"`
	Hidden                 bool   `json:"hidden"`
	Blocked                int    `json:"blocked"`
	WorkspaceKnown         bool   `json:"workspaceKnown"`
	NativeWorkspaceVisible bool   `json:"nativeWorkspaceVisible"`
	NotificationHookIntact bool   `json:"notificationHookIntact"`
	WorkspaceHookIntact    bool   `json:"workspaceHookIntact"`
}
type WebStatus struct {
	InstanceID       string `json:"instanceId"`
	Installed        bool   `json:"installed"`
	WorkspaceID      string `json:"workspaceId"`
	Hidden           bool   `json:"hidden"`
	SoundPatched     bool   `json:"soundPatched"`
	ShortcutGuard    bool   `json:"shortcutGuard"`
	BlockedShortcuts int    `json:"blockedShortcuts"`
	EventDriven      bool   `json:"eventDriven"`
	BlockedSound     int    `json:"blockedSound"`
	SidebarFound     bool   `json:"sidebarFound"`
	SidebarVisible   *bool  `json:"sidebarVisible"`
	ActiveWorkspace  string `json:"activeWorkspace"`
}
type Actual struct {
	Connected   bool          `json:"connected"`
	Version     string        `json:"version,omitempty"`
	WorkspaceID string        `json:"workspaceId"`
	Native      *NativeStatus `json:"native"`
	Web         *WebStatus    `json:"web"`
	Error       string        `json:"error,omitempty"`
}

func (a Actual) applied(hidden bool) bool {
	n, w := a.Native, a.Web
	return a.Connected && n != nil && w != nil && n.Installed && w.Installed && n.Hidden == hidden && w.Hidden == hidden && n.WorkspaceID == a.WorkspaceID && w.WorkspaceID == a.WorkspaceID && n.WorkspaceKnown && n.NotificationHookIntact && n.WorkspaceHookIntact && w.SoundPatched && w.EventDriven && w.ShortcutGuard && n.NativeWorkspaceVisible != hidden && (!w.SidebarFound || (w.SidebarVisible != nil && *w.SidebarVisible != hidden)) && (!hidden || w.ActiveWorkspace != a.WorkspaceID)
}
func (s *Slack) Status(ctx context.Context, c Config) (Actual, error) {
	a := Actual{Connected: true, Version: s.Version, WorkspaceID: c.WorkspaceID}
	main, isolated, err := s.worlds()
	if err != nil {
		return a, err
	}
	var native struct {
		Status  *NativeStatus  `json:"status"`
		Restore *nativeRestore `json:"restore"`
	}
	expression := `(() => { const g = window.__slackWorkNative; return g ? {status: g.status(), restore: {workspaceId:g.workspaceId, workspace:g.workspaces[g.workspaceId], unreads:g.unreads}} : null; })()`
	if _, err = s.cdp.Eval(ctx, expression, isolated, &native); err != nil {
		return a, err
	}
	a.Native = native.Status
	if native.Status != nil && native.Status.WorkspaceID == c.WorkspaceID {
		s.restore = native.Restore
		if s.restore != nil && (len(s.restore.Workspace) == 0 || string(s.restore.Workspace) == "null") {
			s.restore = nil
		}
	}
	_, err = s.cdp.Eval(ctx, "window.__slackWorkWeb?.status() ?? null", main, &a.Web)
	return a, err
}
func (s *Slack) ensureNative(ctx context.Context, main, isolated int, c Config) error {
	var exists bool
	if _, err := s.cdp.Eval(ctx, "!!window.__slackWorkNative", isolated, &exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	notify, err := s.cdp.Eval(ctx, "desktop.notice.notify", isolated, nil)
	if err != nil {
		return err
	}
	module, err := s.cdp.Scope(ctx, notify, "eg")
	if err != nil {
		return err
	}
	invoke, err := s.cdp.Invoke(ctx, module, "function(){return this.FW}", nil, nil)
	if err != nil {
		return err
	}
	electron, err := s.cdp.Scope(ctx, invoke, "r")
	if err != nil {
		return err
	}
	getState, err := s.cdp.Eval(ctx, "desktop.redux.getState", isolated, nil)
	if err != nil {
		return err
	}
	store, err := s.cdp.Scope(ctx, getState, "e")
	if err != nil {
		return err
	}
	var restore *nativeRestore
	if s.restore != nil && s.restore.WorkspaceID == c.WorkspaceID {
		// Reuse presentation metadata only while the web client still lists the workspace.
		var signedIn bool
		selector := fmt.Sprintf(`!!document.querySelector('[data-qa="team_sidebar_item"][data-team="%s"]')`, c.WorkspaceID)
		if _, err = s.cdp.Eval(ctx, selector, main, &signedIn); err != nil {
			return err
		}
		if signedIn {
			restore = s.restore
		}
	}
	var result json.RawMessage
	_, err = s.cdp.Invoke(ctx, electron, nativeHook, []any{store, c, restore}, &result)
	return err
}
func (s *Slack) patchSound(ctx context.Context, main int) error {
	var patched bool
	if _, err := s.cdp.Eval(ctx, "window.__slackWorkWeb?.soundPatched === true", main, &patched); err != nil {
		return err
	}
	if patched {
		return nil
	}
	if err := s.cdp.Call(ctx, "Debugger.enable", nil, nil); err != nil {
		return err
	}
	var breakpoint struct {
		ID string `json:"breakpointId"`
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.cdp.Call(cleanup, "Debugger.resume", nil, nil)
		if breakpoint.ID != "" {
			_ = s.cdp.Call(cleanup, "Debugger.removeBreakpoint", map[string]any{"breakpointId": breakpoint.ID}, nil)
		}
		_ = s.cdp.Call(cleanup, "Debugger.disable", nil, nil)
	}()
	fn, err := s.cdp.Eval(ctx, "window.__slackWorkWeb.soundFunction", main, nil)
	if err != nil {
		return err
	}
	if err = s.cdp.Call(ctx, "Debugger.setBreakpointOnFunctionCall", map[string]any{"objectId": fn.ObjectID}, &breakpoint); err != nil {
		return err
	}
	trigger := make(chan error, 1)
	go func() {
		var result bool
		_, err := s.cdp.Eval(ctx, `(() => { try { window.__slackWorkWeb.soundFunction({selfTeamIds:{teamId:window.__slackWorkWeb.workspaceId}}); } catch {} return true; })()`, main, &result)
		trigger <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.cdp.Done:
		return errors.New("Slack disconnected while installing sound guard")
	case err := <-trigger:
		if err != nil {
			return err
		}
		return errors.New("sound breakpoint was not reached")
	case paused := <-s.paused:
		if len(paused.Frames) == 0 || paused.Frames[0].Name != "canPlayMessageSound" {
			return errors.New("unexpected debugger pause; refusing to patch")
		}
		var result evaluated
		if err = s.cdp.Call(ctx, "Debugger.evaluateOnCallFrame", map[string]any{"callFrameId": paused.Frames[0].ID, "expression": soundHook, "returnByValue": true}, &result); err != nil {
			return err
		}
		var ok bool
		if _, err = decodeEvaluation(result, &ok); err != nil {
			return err
		}
		if !ok {
			return errors.New("incompatible sound adapter")
		}
	}
	if err = s.cdp.Call(ctx, "Debugger.resume", nil, nil); err != nil {
		return err
	}
	select {
	case err = <-trigger:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Slack) Apply(ctx context.Context, c Config, hidden bool) (Actual, error) {
	a, err := s.Status(ctx, c)
	if err != nil {
		return a, err
	}
	if a.applied(hidden) {
		return a, nil
	}
	if a.Web != nil && (!a.Web.EventDriven || !a.Web.ShortcutGuard) {
		return a, errors.New("reload Slack to update its UI guards")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.cdp.Call(cleanup, "Runtime.releaseObjectGroup", map[string]any{"objectGroup": "slack-hours"}, nil)
	}()
	main, isolated, err := s.worlds()
	if err != nil {
		return a, err
	}
	var ready bool
	if _, err = s.cdp.Eval(ctx, readinessHook, main, &ready); err != nil {
		return a, err
	}
	if !ready {
		return a, errors.New("Slack client UI is loading")
	}
	if err = s.ensureNative(ctx, main, isolated, c); err != nil {
		return a, err
	}
	config, _ := json.Marshal(c)
	var ignored json.RawMessage
	if _, err = s.cdp.Eval(ctx, "("+webHook+")("+string(config)+")", main, &ignored); err != nil {
		return a, err
	}
	if err = s.patchSound(ctx, main); err != nil {
		return a, err
	}
	if _, err = s.cdp.Eval(ctx, fmt.Sprintf("window.__slackWorkNative.apply(%t)", hidden), isolated, &ignored); err != nil {
		return a, err
	}
	if _, err = s.cdp.Eval(ctx, fmt.Sprintf("window.__slackWorkWeb.apply(%t)", hidden), main, &ignored); err != nil {
		return a, err
	}
	for i := 0; i < 15; i++ {
		a, err = s.Status(ctx, c)
		if err != nil {
			return a, err
		}
		if a.applied(hidden) {
			return a, nil
		}
		select {
		case <-ctx.Done():
			return a, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return a, errors.New("guards did not reach requested state; inspect slack-hours status --json")
}
func (s *Slack) Test(ctx context.Context) (map[string]map[string]bool, error) {
	main, isolated, err := s.worlds()
	if err != nil {
		return nil, err
	}
	tests := map[string]map[string]bool{}
	for _, entry := range []struct {
		name, expr string
		world      int
	}{{"native", "window.__slackWorkNative.test()", isolated}, {"web", "window.__slackWorkWeb.test()", main}} {
		var values map[string]bool
		if _, err = s.cdp.Eval(ctx, entry.expr, entry.world, &values); err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, errors.New("empty guard self-test result")
		}
		for _, ok := range values {
			if !ok {
				return nil, errors.New("local guard self-test failed")
			}
		}
		tests[entry.name] = values
	}
	return tests, nil
}
