package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

var version = "dev"

const helpText = `slack-hours — local controls for one Slack workspace

  init                 Interactive first-time setup (preserves existing config)
  init --workspace-id ID --workspace-name NAME
                       Noninteractive setup with the default schedule
  configure            Edit configuration interactively
  workspaces [--json]   List signed-in workspaces (no configuration needed)
  hide | show          Override until the next scheduled transition
  auto                 Clear override and follow schedule now
  status [--json]      Live guard status (uses daemon connection when running)
  launch               Start Slack with loopback debugging; never force restart
  install              Install binary, CLI link and login LaunchAgent
  uninstall            Stop scheduler and restore workspace (CLI/config retained)
  schedule             Show schedule
  schedule set --days mon-fri --hours 06:00-18:00 --timezone Europe/Helsinki
  version              Print version
  check                Apply mode and test local guards when hidden
  daemon               Run event-driven reconciler in foreground

After quitting Slack, use 'slack-hours launch'. The scheduler leaves Slack closed.
Runtime hooks are experimental and have startup/reload gaps.
`

func printJSON(v any) { enc := json.NewEncoder(os.Stdout); enc.SetIndent("", "  "); _ = enc.Encode(v) }
func parseSchedule(args []string, s Schedule) (Schedule, error) {
	seen := map[string]bool{}
	for len(args) > 0 {
		if len(args) < 2 {
			return s, errors.New("schedule flags require values")
		}
		key, value := args[0], args[1]
		args = args[2:]
		if seen[key] {
			return s, errors.New("duplicate schedule flag")
		}
		seen[key] = true
		switch key {
		case "--days":
			if value == "mon-fri" {
				s.Days = []string{"mon", "tue", "wed", "thu", "fri"}
			} else {
				s.Days = strings.Split(value, ",")
			}
		case "--hours":
			parts := strings.Split(value, "-")
			if len(parts) != 2 {
				return s, errors.New("hours must be HH:MM-HH:MM")
			}
			s.Start, s.End = parts[0], parts[1]
		case "--timezone":
			s.TimeZone = value
		default:
			return s, fmt.Errorf("unknown schedule flag %s", key)
		}
	}
	return s, s.validate()
}
func runCLI(ctx context.Context, args []string, p Paths) int {
	jsonOutput := false
	filtered := []string{}
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	args = filtered
	cmd := "status"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	fail := func(err error) int {
		if jsonOutput {
			printJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "slack-hours:", err)
		}
		return 1
	}
	if cmd == "version" || cmd == "--version" {
		fmt.Println("slack-hours", version)
		return 0
	}
	if cmd == "help" || cmd == "--help" {
		fmt.Print(helpText)
		return 0
	}
	if cmd != "schedule" && cmd != "init" && len(args) > 0 {
		return fail(errors.New("unexpected arguments"))
	}
	if cmd == "daemon" {
		if err := runDaemon(ctx, p); err != nil {
			return fail(err)
		}
		return 0
	}
	if cmd == "configure" || (cmd == "init" && len(args) == 0 && !jsonOutput) {
		_, statErr := os.Stat(p.configFile())
		if cmd == "configure" || errors.Is(statErr, os.ErrNotExist) {
			if jsonOutput || !term.IsTerminal(os.Stdin.Fd()) {
				return fail(errors.New("interactive setup requires a terminal; use init --workspace-id ID --workspace-name NAME for scripts"))
			}
			if err := configure(ctx, p, os.Stdin, os.Stdout); err != nil {
				return fail(err)
			}
			return 0
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if cmd == "workspaces" || cmd == "launch" {
		c := defaultConfig()
		readErr := readJSON(p.configFile(), &c)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fail(readErr)
		}
		if cmd == "workspaces" {
			workspaces, err := listWizardWorkspaces(ctx, c.Port)
			if err != nil {
				return fail(fmt.Errorf("cannot list workspaces: %w; run slack-hours launch first", err))
			}
			if jsonOutput {
				printJSON(workspaces)
			} else {
				for _, workspace := range workspaces {
					fmt.Printf("%s  %s\n", workspace.ID, workspace.Name)
				}
			}
			return 0
		}
		if errors.Is(readErr, os.ErrNotExist) {
			if err := startSlack(ctx, c.Port); err != nil {
				return fail(err)
			}
			if jsonOutput {
				printJSON(map[string]bool{"connected": true, "configured": false})
			} else {
				fmt.Println("Slack is ready for setup. Run slack-hours init.")
			}
			return 0
		}
	}
	if cmd == "init" {
		unlock, err := lockFile(ctx, filepath.Join(p.StateDir, "apply.flock"), true)
		if err != nil {
			return fail(err)
		}
		defer unlock()
		if _, err = os.Stat(p.configFile()); errors.Is(err, os.ErrNotExist) {
			c := defaultConfig()
			seen := map[string]bool{}
			var scheduleArgs []string
			for len(args) > 0 {
				if len(args) < 2 || seen[args[0]] {
					return fail(errors.New("init requires unique flags with values"))
				}
				seen[args[0]] = true
				switch args[0] {
				case "--workspace-id":
					c.WorkspaceID = args[1]
				case "--workspace-name":
					c.WorkspaceName = args[1]
				case "--days", "--hours", "--timezone":
					scheduleArgs = append(scheduleArgs, args[:2]...)
				default:
					return fail(fmt.Errorf("unknown init flag %s", args[0]))
				}
				args = args[2:]
			}
			if len(scheduleArgs) > 0 {
				c.Schedule, err = parseSchedule(scheduleArgs, c.Schedule)
				if err != nil {
					return fail(err)
				}
			}
			if err = c.validate(); err != nil {
				return fail(fmt.Errorf("init requires --workspace-id and --workspace-name: %w", err))
			}
			err = writeJSON(p.configFile(), c)
		}
		if err != nil {
			return fail(err)
		}
		if _, _, err = p.load(); err != nil {
			return fail(err)
		}
		if jsonOutput {
			printJSON(map[string]string{"configPath": p.configFile()})
		} else {
			fmt.Println("Configuration:", p.configFile())
			fmt.Println("Run slack-hours configure to change settings, or slack-hours install to start the scheduler.")
		}
		return 0
	}
	config, _, err := p.load()
	if err != nil {
		return fail(err)
	}
	req := Request{Command: cmd}
	switch cmd {
	case "install":
		if err = installService(ctx, p); err != nil {
			return fail(err)
		}
		binary, link, plist := servicePaths()
		if jsonOutput {
			printJSON(map[string]string{"binary": binary, "cli": link, "launchAgent": plist})
		} else {
			fmt.Printf("Binary: %s\nCLI: %s\nScheduler: %s\n", binary, link, plist)
		}
		return 0
	case "uninstall":
		if err = stopService(ctx); err != nil {
			return fail(err)
		}
		unlock, err := lockFile(ctx, filepath.Join(p.StateDir, "apply.flock"), true)
		if err != nil {
			return fail(err)
		}
		defer unlock()
		if err = removeFile(p.overrideFile()); err != nil {
			return fail(err)
		}
		slack, connectErr := connectSlack(ctx, config.Port)
		if connectErr == nil {
			_, err = slack.Apply(ctx, config, false)
			slack.Close()
			if err != nil {
				return fail(err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Could not verify restoration. If Slack is open, quit and reopen it normally to remove runtime guards.")
		}
		if jsonOutput {
			printJSON(map[string]bool{"schedulerInstalled": false})
		} else {
			fmt.Println("Scheduler removed; CLI and configuration retained.")
		}
		return 0
	case "schedule":
		if len(args) == 0 {
			if jsonOutput {
				printJSON(config.Schedule)
			} else {
				fmt.Printf("Visible %s %s–%s %s; hidden otherwise.\n", strings.Join(config.Schedule.Days, ","), config.Schedule.Start, config.Schedule.End, config.Schedule.TimeZone)
			}
			return 0
		}
		if args[0] != "set" {
			return fail(errors.New("unknown schedule command"))
		}
		s, err := parseSchedule(args[1:], config.Schedule)
		if err != nil {
			return fail(err)
		}
		req.Schedule = &s
	case "status", "hide", "show", "auto", "check", "launch":
	default:
		return fail(fmt.Errorf("unknown command %q; use slack-hours help", cmd))
	}
	var result Result
	if cmd == "launch" {
		result, err = launchSlack(ctx, p)
	} else {
		result, err = dispatch(ctx, p, req)
	}
	if cmd == "status" {
		installed := serviceInstalled(ctx)
		if jsonOutput {
			printJSON(struct {
				Result
				SchedulerInstalled bool `json:"schedulerInstalled"`
			}{result, installed})
		} else {
			visibility := "visible"
			if result.Target.Hidden {
				visibility = "hidden"
			}
			fmt.Printf("%s: %s requested (%s)\n", result.Workspace, visibility, result.Target.Mode)
			loc, _ := time.LoadLocation(config.Schedule.TimeZone)
			fmt.Printf("Next transition / override expiry: %s %s\n", result.Target.Until.In(loc).Format("Mon 2 Jan, 15:04"), config.Schedule.TimeZone)
			if result.Healthy {
				fmt.Println("Live guards: verified (event-driven)")
			} else {
				fmt.Println("Live guards: NOT VERIFIED")
			}
			fmt.Printf("Scheduler installed: %t\n", installed)
			if err != nil {
				fmt.Println(err)
			}
		}
		if err != nil || !result.Healthy {
			return 1
		}
		return 0
	}
	if err != nil {
		if cmd == "hide" || cmd == "show" || cmd == "auto" || cmd == "schedule" {
			err = fmt.Errorf("could not apply requested mode; inspect status/config before retrying: %w", err)
		}
		return fail(err)
	}
	if jsonOutput {
		printJSON(result)
		return 0
	}
	if cmd == "check" {
		if result.Tests != nil {
			fmt.Println("Local notification, sound and workspace guard checks passed. No Slack messages sent.")
		} else {
			fmt.Println("Visible mode verified; use hide then check to test suppression.")
		}
		return 0
	}
	state := "restored"
	if result.Target.Hidden {
		state = "hidden and muted"
	}
	loc, _ := time.LoadLocation(result.Schedule.TimeZone)
	fmt.Printf("%s %s (%s). Next transition: %s %s.\n", result.Workspace, state, result.Target.Mode, result.Target.Until.In(loc).Format("Mon 2 Jan, 15:04"), result.Schedule.TimeZone)
	return 0
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runCLI(ctx, os.Args[1:], defaultPaths())
	cancel()
	os.Exit(code)
}
