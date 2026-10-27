package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errSetupCancelled = errors.New("setup cancelled; configuration unchanged")

func workspaceID(value string) string {
	if validID.MatchString(value) {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "app.slack.com" && !strings.HasSuffix(u.Hostname(), ".slack.com")) {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "client" && validID.MatchString(parts[1]) {
		return parts[1]
	}
	return ""
}

// Prompting happens before taking the mutation lock; saving checks for concurrent edits.
func configure(ctx context.Context, p Paths, input io.Reader, output io.Writer) error {
	original, err := os.ReadFile(p.configFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c := defaultConfig()
	if err == nil {
		if err := json.Unmarshal(original, &c); err != nil {
			return fmt.Errorf("read existing configuration: %w", err)
		}
	}
	c, err = configurationWizard(ctx, input, output, c)
	if err != nil {
		return err
	}
	saveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	unlock, err := lockFile(saveCtx, filepath.Join(p.StateDir, "apply.flock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := os.ReadFile(p.configFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("configuration changed during setup; run configure again")
	}
	if err := saveCtx.Err(); err != nil {
		return err
	}
	if err := writeJSON(p.configFile(), c); err != nil {
		return err
	}
	fmt.Fprintln(output, "Saved configuration:", p.configFile())
	fmt.Fprintln(output, "Existing hide/show overrides are unchanged. Use 'slack-hours auto' to follow the schedule.")
	fmt.Fprintln(output, "If you changed the workspace or port, quit Slack and run 'slack-hours launch'.")
	fmt.Fprintln(output, "For first-time setup, run 'slack-hours install', then quit Slack and run 'slack-hours launch'.")
	return nil
}

func configurationWizard(ctx context.Context, input io.Reader, output io.Writer, c Config) (Config, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan string)
	scanErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		scanErrors <- scanner.Err()
		close(lines)
	}()
	ask := func(label, fallback string, accept func(string) error) error {
		for {
			fmt.Fprint(output, label)
			if fallback != "" {
				fmt.Fprintf(output, " [%s]", fallback)
			}
			fmt.Fprint(output, ": ")
			var value string
			select {
			case <-ctx.Done():
				return errSetupCancelled
			case line, ok := <-lines:
				if !ok {
					if err := <-scanErrors; err != nil {
						return err
					}
					return errSetupCancelled
				}
				value = strings.TrimSpace(line)
			}
			if value == "" {
				value = fallback
			}
			if err := accept(value); err != nil {
				fmt.Fprintln(output, err)
				continue
			}
			return nil
		}
	}
	fmt.Fprintln(output, "Configure slack-hours\nPress Enter to keep a default. Ctrl+C cancels without saving.")
	fmt.Fprintln(output, "Find your workspace ID after /client/ in its Slack web URL.")
	if err := ask("Workspace ID or URL", c.WorkspaceID, func(v string) error {
		id := workspaceID(v)
		if id == "" {
			return errors.New("enter a workspace ID (T… or E…) or an https://app.slack.com/client/… URL")
		}
		c.WorkspaceID = id
		return nil
	}); err != nil {
		return c, err
	}
	if err := ask("Workspace name (as shown in Slack)", c.WorkspaceName, func(v string) error {
		if v == "" {
			return errors.New("enter a workspace name")
		}
		c.WorkspaceName = v
		return nil
	}); err != nil {
		return c, err
	}
	for _, field := range []struct{ label, flag, fallback string }{
		{"Visible days (mon-fri or comma-separated days)", "--days", strings.Join(c.Schedule.Days, ",")},
		{"Visible hours (HH:MM-HH:MM, same day)", "--hours", c.Schedule.Start + "-" + c.Schedule.End},
		{"Timezone (e.g. Europe/London)", "--timezone", c.Schedule.TimeZone},
	} {
		if err := ask(field.label, field.fallback, func(v string) error {
			s, err := parseSchedule([]string{field.flag, v}, c.Schedule)
			if err == nil {
				c.Schedule = s
			}
			return err
		}); err != nil {
			return c, err
		}
	}
	if err := ask("Slack debugging port", strconv.Itoa(c.Port), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1024 || n > 65535 {
			return errors.New("enter a port from 1024 to 65535")
		}
		c.Port = n
		return nil
	}); err != nil {
		return c, err
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	fmt.Fprintf(output, "\nWorkspace: %s (%s)\nVisible: %s, %s-%s %s\nHidden at all other times. Debugging port: %d\n\n", c.WorkspaceName, c.WorkspaceID, strings.Join(c.Schedule.Days, ","), c.Schedule.Start, c.Schedule.End, c.Schedule.TimeZone, c.Port)
	confirmed := false
	if err := ask("Save configuration? (y/N)", "n", func(v string) error {
		switch strings.ToLower(v) {
		case "y", "yes":
			confirmed = true
		case "n", "no":
		default:
			return errors.New("enter y or n")
		}
		return nil
	}); err != nil {
		return c, err
	}
	if !confirmed {
		return c, errSetupCancelled
	}
	return c, nil
}
