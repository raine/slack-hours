package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type wireMessage struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type CDP struct {
	conn     *websocket.Conn
	sendMu   sync.Mutex
	mu       sync.Mutex
	pending  map[int64]chan wireMessage
	sequence atomic.Int64
	Calls    atomic.Uint64
	Done     chan struct{}
	onEvent  func(string, json.RawMessage)
}

func newCDP(conn *websocket.Conn, event func(string, json.RawMessage)) *CDP {
	c := &CDP{conn: conn, pending: map[int64]chan wireMessage{}, Done: make(chan struct{}), onEvent: event}
	conn.SetReadLimit(8 << 20)
	go c.readLoop()
	return c
}
func (c *CDP) readLoop() {
	defer close(c.Done)
	defer c.conn.Close()
	for {
		var message wireMessage
		if err := c.conn.ReadJSON(&message); err != nil {
			return
		}
		if message.ID != 0 {
			c.mu.Lock()
			ch := c.pending[message.ID]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- message:
				default:
				}
			}
		} else if c.onEvent != nil {
			c.onEvent(message.Method, message.Params)
		}
	}
}
func (c *CDP) Close() { _ = c.conn.Close() }
func (c *CDP) Call(ctx context.Context, method string, params any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	id := c.sequence.Add(1)
	ch := make(chan wireMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	c.sendMu.Lock()
	if err := ctx.Err(); err != nil {
		c.sendMu.Unlock()
		return err
	}
	deadline, _ := ctx.Deadline()
	_ = c.conn.SetWriteDeadline(deadline)
	err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.sendMu.Unlock()
	if err != nil {
		return errors.New("Slack debugger write failed")
	}
	c.Calls.Add(1)
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.Done:
		return errors.New("Slack debugger disconnected")
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("CDP %s: %s", method, msg.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

type Remote struct {
	ObjectID    string          `json:"objectId,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Description string          `json:"description,omitempty"`
}
type evaluated struct {
	Result    Remote          `json:"result"`
	Exception json.RawMessage `json:"exceptionDetails"`
}

func (c *CDP) Eval(ctx context.Context, expression string, world int, value any) (Remote, error) {
	params := map[string]any{"expression": expression, "contextId": world, "returnByValue": value != nil, "awaitPromise": true, "objectGroup": "slack-hours"}
	var r evaluated
	if err := c.Call(ctx, "Runtime.evaluate", params, &r); err != nil {
		return Remote{}, err
	}
	return decodeEvaluation(r, value)
}
func decodeEvaluation(r evaluated, value any) (Remote, error) {
	if len(r.Exception) > 0 {
		var exception struct {
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		}
		_ = json.Unmarshal(r.Exception, &exception)
		// Only surface known adapter errors; arbitrary app exceptions may contain data.
		for _, message := range []string{"Configured workspace is absent", "Unsupported Slack sound selectors", "Slack module cache is unavailable"} {
			if strings.HasPrefix(exception.Exception.Description, "Error: "+message+"\n") {
				return Remote{}, errors.New(message)
			}
		}
		return Remote{}, errors.New("Slack runtime evaluation failed; adapter may be incompatible")
	}
	if value != nil && len(r.Result.Value) > 0 {
		if err := json.Unmarshal(r.Result.Value, value); err != nil {
			return Remote{}, err
		}
	}
	return r.Result, nil
}
func (c *CDP) Invoke(ctx context.Context, object Remote, declaration string, args []any, value any) (Remote, error) {
	arguments := []any{}
	for _, arg := range args {
		if remote, ok := arg.(Remote); ok {
			arguments = append(arguments, map[string]any{"objectId": remote.ObjectID})
		} else {
			arguments = append(arguments, map[string]any{"value": arg})
		}
	}
	var r evaluated
	err := c.Call(ctx, "Runtime.callFunctionOn", map[string]any{"objectId": object.ObjectID, "functionDeclaration": declaration, "arguments": arguments, "returnByValue": value != nil, "awaitPromise": true, "objectGroup": "slack-hours"}, &r)
	if err != nil {
		return Remote{}, err
	}
	return decodeEvaluation(r, value)
}

type property struct {
	Name  string `json:"name"`
	Value Remote `json:"value"`
}
type properties struct {
	Result   []property `json:"result"`
	Internal []property `json:"internalProperties"`
}

func (c *CDP) props(ctx context.Context, object Remote) (properties, error) {
	var p properties
	err := c.Call(ctx, "Runtime.getProperties", map[string]any{"objectId": object.ObjectID, "ownProperties": true}, &p)
	return p, err
}
func (c *CDP) Scope(ctx context.Context, fn Remote, name string) (Remote, error) {
	p, err := c.props(ctx, fn)
	if err != nil {
		return Remote{}, err
	}
	var scopes Remote
	for _, prop := range p.Internal {
		if prop.Name == "[[Scopes]]" {
			scopes = prop.Value
		}
	}
	if scopes.ObjectID == "" {
		return Remote{}, errors.New("Slack function has no inspectable scope")
	}
	list, err := c.props(ctx, scopes)
	if err != nil {
		return Remote{}, err
	}
	for _, scope := range list.Result {
		if scope.Value.ObjectID == "" {
			continue
		}
		vars, err := c.props(ctx, scope.Value)
		if err != nil {
			return Remote{}, err
		}
		for _, prop := range vars.Result {
			if prop.Name == name {
				return prop.Value, nil
			}
		}
	}
	return Remote{}, fmt.Errorf("unsupported Slack preload: missing scope variable %s", name)
}

var errMultipleClients = errors.New("multiple Slack client windows; close extra windows before attaching")
var errClientNotReady = errors.New("Slack client is not ready or signed in")

func discovery(ctx context.Context, port int) (string, string, error) {
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("refusing debugger redirect") }}
	get := func(path string, out any) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/json/%s", port, path), nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("debug endpoint unavailable")
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	var version struct {
		UserAgent string `json:"User-Agent"`
	}
	if err := get("version", &version); err != nil {
		return "", "", errors.New("Slack debugging unavailable; use slack-hours launch (quit Slack first if already running)")
	}
	if !strings.Contains(version.UserAgent, "Slack/") {
		return "", "", errors.New("debug port belongs to another app; refusing to attach")
	}
	var targets []struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := get("list", &targets); err != nil {
		return "", "", err
	}
	endpoint := ""
	for _, t := range targets {
		u, err := url.Parse(t.URL)
		if err == nil && t.Type == "page" && u.Scheme == "https" && u.Host == "app.slack.com" && (u.Path == "/client" || strings.HasPrefix(u.Path, "/client/")) {
			if endpoint != "" {
				return "", "", errMultipleClients
			}
			endpoint = t.Socket
		}
	}
	if endpoint == "" {
		return "", "", errClientNotReady
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "ws" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.Port() != strconv.Itoa(port) || u.User != nil {
		return "", "", errors.New("refusing non-local debugger endpoint")
	}
	v := strings.Fields(strings.SplitN(version.UserAgent, "Slack/", 2)[1])[0]
	return endpoint, v, nil
}
