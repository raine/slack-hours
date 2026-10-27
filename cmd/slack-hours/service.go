package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const serviceLabel = "local.slack-hours"

func serviceDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }
func servicePaths() (binary, link, plist string) {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/lib/slack-hours/slack-hours"), filepath.Join(home, ".local/bin/slack-hours"), filepath.Join(home, "Library/LaunchAgents/"+serviceLabel+".plist")
}
func command(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, bytes.TrimSpace(output))
	}
	return nil
}
func serviceInstalled(ctx context.Context) bool {
	return exec.CommandContext(ctx, "/bin/launchctl", "print", serviceDomain()+"/"+serviceLabel).Run() == nil
}
func escapeXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func launchAgentXML(binary string, p Paths) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>daemon</string></array>
<key>EnvironmentVariables</key><dict>
<key>SLACK_HOURS_CONFIG_DIR</key><string>%s</string>
<key>SLACK_HOURS_STATE_DIR</key><string>%s</string>
</dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>15</integer>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, serviceLabel, escapeXML(binary), escapeXML(p.ConfigDir), escapeXML(p.StateDir), escapeXML(filepath.Join(p.StateDir, "daemon.log")), escapeXML(filepath.Join(p.StateDir, "daemon-error.log")))
}
func stopService(ctx context.Context) error {
	if serviceInstalled(ctx) {
		if err := command(ctx, "/bin/launchctl", "bootout", serviceDomain()+"/"+serviceLabel); err != nil {
			return err
		}
	}
	_, _, plist := servicePaths()
	return removeFile(plist)
}
func installService(ctx context.Context, p Paths) error {
	if runtime.GOOS != "darwin" {
		return errors.New("LaunchAgents require macOS")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	binary, link, plist := servicePaths()
	if info, err := os.Lstat(link); err == nil {
		target, linkErr := os.Readlink(link)
		if info.Mode()&os.ModeSymlink == 0 || linkErr != nil || (target != binary && target != exe) {
			return fmt.Errorf("refusing to replace existing %s", link)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if err = stopService(ctx); err != nil {
		return err
	}
	if err = atomicWrite(binary, data, 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	if err = removeFile(link); err != nil {
		return err
	}
	if err = os.Symlink(binary, link); err != nil {
		return err
	}
	if err = os.MkdirAll(p.StateDir, 0700); err != nil {
		return err
	}
	if err = atomicWrite(plist, []byte(launchAgentXML(binary, p)), 0600); err != nil {
		return err
	}
	if err = command(ctx, "/usr/bin/plutil", "-lint", plist); err != nil {
		return err
	}
	// launchd may retain the job while its previous process exits.
	for i := 0; ; i++ {
		err = command(ctx, "/bin/launchctl", "bootstrap", serviceDomain(), plist)
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 5 || i >= 29 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
func launchSlack(ctx context.Context, p Paths) (Result, error) {
	if runtime.GOOS != "darwin" {
		return Result{}, errors.New("launch requires macOS")
	}
	config, _, err := p.load()
	if err != nil {
		return Result{}, err
	}
	if _, _, err = discovery(ctx, config.Port); err != nil {
		err = exec.CommandContext(ctx, "/usr/bin/pgrep", "-x", "Slack").Run()
		if err == nil {
			return Result{}, errors.New("Slack is running without usable debugging; quit Slack, then run slack-hours launch")
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return Result{}, err
		}
		if err = command(ctx, "/usr/bin/open", "-a", "/Applications/Slack.app", "--args", "--remote-debugging-address=127.0.0.1", fmt.Sprintf("--remote-debugging-port=%d", config.Port)); err != nil {
			return Result{}, err
		}
		for until := time.Now().Add(15 * time.Second); time.Now().Before(until); {
			if _, _, err = discovery(ctx, config.Port); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return dispatch(ctx, p, Request{Command: "reconcile"})
}
