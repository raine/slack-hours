package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

type Request struct {
	Command  string    `json:"command"`
	Schedule *Schedule `json:"schedule,omitempty"`
}
type Stats struct {
	Connections uint64 `json:"connections"`
	CDPCalls    uint64 `json:"cdpCalls"`
}
type Result struct {
	Workspace string                     `json:"workspace"`
	Schedule  Schedule                   `json:"schedule"`
	Target    Target                     `json:"target"`
	Healthy   bool                       `json:"healthy"`
	Actual    Actual                     `json:"actual"`
	Tests     map[string]map[string]bool `json:"tests,omitempty"`
	Stats     Stats                      `json:"stats"`
}
type Controller struct {
	Paths                    Paths
	slack                    *Slack
	restore                  *nativeRestore
	connections, closedCalls uint64
}

func (c *Controller) Close() {
	if c.slack != nil {
		c.restore = c.slack.restore
		c.closedCalls += c.slack.cdp.Calls.Load()
		c.slack.Close()
		c.slack = nil
	}
}
func (c *Controller) Changes() <-chan struct{} {
	if c.slack != nil {
		return c.slack.Changed
	}
	return nil
}
func (c *Controller) Disconnected() <-chan struct{} {
	if c.slack != nil {
		return c.slack.cdp.Done
	}
	return nil
}
func (c *Controller) stats() Stats {
	calls := c.closedCalls
	if c.slack != nil {
		calls += c.slack.cdp.Calls.Load()
	}
	return Stats{c.connections, calls}
}
func (c *Controller) ensure(ctx context.Context, config Config) error {
	if c.slack != nil {
		if c.slack.Port != config.Port {
			c.Close()
		} else {
			select {
			case <-c.slack.cdp.Done:
				c.Close()
			default:
			}
		}
	}
	if c.slack == nil {
		s, err := connectSlack(ctx, config.Port)
		if err != nil {
			return err
		}
		s.restore = c.restore
		c.slack = s
		c.connections++
	}
	return nil
}
func (c *Controller) Do(ctx context.Context, req Request) (result Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := lockFile(ctx, filepath.Join(c.Paths.StateDir, "apply.flock"), true)
	if err != nil {
		return result, err
	}
	defer unlock()
	defer func() {
		result.Stats = c.stats()
		if err != nil {
			result.Actual.Error = err.Error()
			result.Healthy = false
		}
	}()
	config, override, err := c.Paths.load()
	if err != nil {
		return result, err
	}
	now := time.Now()
	switch req.Command {
	case "hide", "show":
		target := desired(now, config, Override{})
		override = Override{req.Command, target.Until}
		if err = writeJSON(c.Paths.overrideFile(), override); err != nil {
			return result, err
		}
	case "auto":
		override = Override{}
		if err = removeFile(c.Paths.overrideFile()); err != nil {
			return result, err
		}
	case "schedule":
		if req.Schedule == nil {
			return result, errors.New("missing schedule")
		}
		config.Schedule = *req.Schedule
		if err = config.validate(); err != nil {
			return result, err
		}
		if err = writeJSON(c.Paths.configFile(), config); err != nil {
			return result, err
		}
		override = Override{}
		if err = removeFile(c.Paths.overrideFile()); err != nil {
			return result, err
		}
	case "reconcile", "status", "check":
	default:
		return result, errors.New("unsupported controller command")
	}
	result.Workspace = config.WorkspaceName
	result.Schedule = config.Schedule
	result.Target = desired(now, config, override)
	result.Actual.WorkspaceID = config.WorkspaceID
	if err = c.ensure(ctx, config); err != nil {
		return result, err
	}
	if req.Command == "status" {
		result.Actual, err = c.slack.Status(ctx, config)
	} else {
		result.Actual, err = c.slack.Apply(ctx, config, result.Target.Hidden)
	}
	if err != nil {
		return result, err
	}
	result.Healthy = result.Actual.applied(result.Target.Hidden)
	if req.Command == "check" && result.Target.Hidden {
		result.Tests, err = c.slack.Test(ctx)
	}
	return result, err
}
