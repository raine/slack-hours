package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Response struct {
	Result Result `json:"result"`
	Error  string `json:"error,omitempty"`
}
type work struct {
	request Request
	reply   chan Response
}
type engine interface {
	Do(context.Context, Request) (Result, error)
	Changes() <-chan struct{}
	Disconnected() <-chan struct{}
	Close()
}

func remoteRequest(ctx context.Context, p Paths, req Request) (Response, bool, error) {
	var response Response
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", p.socketFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return response, false, nil
		}
		return response, true, fmt.Errorf("daemon connection failed: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(40 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return response, true, err
	}
	err = json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&response)
	if err != nil {
		return response, true, fmt.Errorf("daemon response failed (not retrying the command): %w", err)
	}
	return response, true, nil
}
func dispatch(ctx context.Context, p Paths, req Request) (Result, error) {
	response, available, err := remoteRequest(ctx, p, req)
	if err != nil {
		return Result{}, err
	}
	if available {
		if response.Error != "" {
			return response.Result, errors.New(response.Error)
		}
		return response.Result, nil
	}
	controller := &Controller{Paths: p}
	defer controller.Close()
	return controller.Do(ctx, req)
}
func serveRequests(ctx context.Context, listener net.Listener, requests chan<- work) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
			var req Request
			if err := json.NewDecoder(io.LimitReader(conn, 64<<10)).Decode(&req); err != nil {
				return
			}
			job := work{req, make(chan Response, 1)}
			select {
			case requests <- job:
			case <-ctx.Done():
				return
			}
			select {
			case response := <-job.reply:
				_ = json.NewEncoder(conn).Encode(response)
			case <-ctx.Done():
			}
		}()
	}
}
func runDaemon(ctx context.Context, p Paths) error {
	unlock, err := lockFile(ctx, filepath.Join(p.StateDir, "daemon.flock"), false)
	if err != nil {
		return err
	}
	defer unlock()
	if info, err := os.Lstat(p.socketFile()); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace non-socket control path")
		}
		if err = os.Remove(p.socketFile()); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", p.socketFile())
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(p.socketFile(), 0600); err != nil {
		return err
	}
	requests := make(chan work, 32)
	go serveRequests(ctx, listener, requests)
	c := &Controller{Paths: p}
	defer c.Close()
	return runLoop(ctx, c, requests, time.Minute, os.Stdout, func(summary any) { _ = writeJSON(filepath.Join(p.StateDir, "last-result.json"), summary) })
}

// The steady state sleeps until a request, renderer event, schedule boundary,
// or the low-frequency health check. Retry backoff is only used after errors.
func runLoop(ctx context.Context, c engine, requests <-chan work, healthInterval time.Duration, log io.Writer, persist func(any)) error {
	health := time.NewTicker(healthInterval)
	defer health.Stop()
	next := time.NewTimer(time.Hour)
	defer next.Stop()
	last := ""
	retry := time.Second
	reconcile := func(req Request) Response {
		result, err := c.Do(ctx, req)
		response := Response{Result: result}
		if err != nil {
			response.Error = err.Error()
		}
		// Observational queries must not postpone pending recovery or transitions.
		if req.Command == "status" {
			return response
		}
		summary := struct {
			Healthy bool      `json:"healthy"`
			Hidden  bool      `json:"hidden"`
			Mode    string    `json:"mode"`
			Until   time.Time `json:"until"`
			Error   string    `json:"error,omitempty"`
		}{result.Healthy, result.Target.Hidden, result.Target.Mode, result.Target.Until, response.Error}
		encoded, _ := json.Marshal(summary)
		if string(encoded) != last {
			fmt.Fprintln(log, time.Now().UTC().Format(time.RFC3339), string(encoded))
			if persist != nil {
				persist(summary)
			}
			last = string(encoded)
		}
		var duration time.Duration
		if err != nil || !result.Healthy {
			duration = retry
			if retry < 30*time.Second {
				retry *= 2
			}
			if retry > 30*time.Second {
				retry = 30 * time.Second
			}
		} else {
			retry = time.Second
			duration = time.Until(result.Target.Until)
			if result.Target.Until.IsZero() {
				duration = healthInterval
			}
		}
		if duration < time.Millisecond {
			duration = time.Millisecond
		}
		next.Reset(duration)
		return response
	}
	reconcile(Request{Command: "reconcile"})
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-requests:
			job.reply <- reconcile(job.request)
		case <-c.Changes():
			reconcile(Request{Command: "reconcile"})
		case <-c.Disconnected():
			reconcile(Request{Command: "reconcile"})
		case <-next.C:
			reconcile(Request{Command: "reconcile"})
		case <-health.C:
			reconcile(Request{Command: "reconcile"})
		}
	}
}
