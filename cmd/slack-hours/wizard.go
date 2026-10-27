package main

import (
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

	"charm.land/huh/v2"
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
	return configureWithWizard(ctx, p, input, output, configurationWizard)
}

func configureWithWizard(ctx context.Context, p Paths, input io.Reader, output io.Writer, wizard func(context.Context, io.Reader, io.Writer, Config) (Config, error)) error {
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
	previous := c
	c, err = wizard(ctx, input, output, c)
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
	if len(original) == 0 {
		fmt.Fprintln(output, "Next: run slack-hours install, then slack-hours status.")
	} else if previous.WorkspaceID != c.WorkspaceID || previous.Port != c.Port {
		fmt.Fprintln(output, "Quit Slack, then run slack-hours launch to apply the new workspace.")
	} else {
		fmt.Fprintln(output, "The running scheduler will apply your settings within a minute. Run slack-hours status to verify.")
	}
	var override Override
	if readJSON(p.overrideFile(), &override) == nil && override.ExpiresAt.After(time.Now()) && (override.Mode == "hide" || override.Mode == "show") {
		fmt.Fprintf(output, "Your %s override lasts until %s. Run slack-hours auto to follow the schedule now.\n", override.Mode, override.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Fprintln(output, "Open Slack with slack-hours launch from now on.")
	return nil
}

type wizardValues struct {
	Workspace, Name, Hours, TimeZone, Port string
	Days                                   []string
	Save                                   bool
}

func (v *wizardValues) config() (Config, error) {
	c := defaultConfig()
	c.WorkspaceID, c.WorkspaceName = workspaceID(strings.TrimSpace(v.Workspace)), strings.TrimSpace(v.Name)
	var err error
	c.Port, err = strconv.Atoi(strings.TrimSpace(v.Port))
	if err != nil {
		return c, errors.New("enter a port from 1024 to 65535")
	}
	c.Schedule, err = parseSchedule([]string{"--days", strings.Join(v.Days, ","), "--hours", strings.TrimSpace(v.Hours), "--timezone", strings.TrimSpace(v.TimeZone)}, c.Schedule)
	if err != nil {
		return c, err
	}
	return c, c.validate()
}

func wizardForm(v *wizardValues, workspaces []wizardWorkspace) *huh.Form {
	options := make([]huh.Option[string], 0, len(workspaces))
	nameCounts := map[string]int{}
	for _, workspace := range workspaces {
		nameCounts[workspace.Name]++
	}
	for _, workspace := range workspaces {
		label := workspace.Name
		if nameCounts[label] > 1 {
			label += " (" + workspace.ID + ")"
		}
		options = append(options, huh.NewOption(label, workspace.ID))
	}
	workspaceName := func() string {
		for _, workspace := range workspaces {
			if workspace.ID == v.Workspace {
				return workspace.Name
			}
		}
		return ""
	}
	review := func() string {
		return fmt.Sprintf("%s\n\nVisible: %s\nHours: %s · %s\n\nHidden at all other times.", workspaceName(), friendlyDays(v.Days), strings.TrimSpace(v.Hours), strings.TrimSpace(v.TimeZone))
	}
	scheduleField := func(flag string) func(string) error {
		return func(value string) error {
			_, err := parseSchedule([]string{flag, strings.TrimSpace(value)}, defaultConfig().Schedule)
			return err
		}
	}
	workspaceDescription := "Choose a signed-in workspace. Type / to filter the list."
	if len(workspaces) == 1 {
		workspaceDescription += "\nOnly one workspace is signed in. Add another in Slack so we can switch away when hiding this one."
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Which workspace should follow your working hours?").Description(workspaceDescription).Options(options...).Value(&v.Workspace),
		).Title("slack-hours · 1/4 · Workspace"),
		huh.NewGroup(
			huh.NewMultiSelect[string]().Title("Working days").Description("Space toggles a day. Enter continues.").Options(
				huh.NewOption("Monday", "mon"), huh.NewOption("Tuesday", "tue"), huh.NewOption("Wednesday", "wed"), huh.NewOption("Thursday", "thu"), huh.NewOption("Friday", "fri"), huh.NewOption("Saturday", "sat"), huh.NewOption("Sunday", "sun"),
			).Height(9).Value(&v.Days).Validate(func(days []string) error {
				if len(days) == 0 {
					return errors.New("choose at least one day")
				}
				return nil
			}),
		).Title("slack-hours · 2/4 · Days").Description("The workspace stays hidden on unselected days."),
		huh.NewGroup(
			huh.NewInput().Title("Visible hours").Description("24-hour time, starting and ending on the same day.").Placeholder("09:00-17:00").Value(&v.Hours).Validate(scheduleField("--hours")),
			huh.NewInput().Title("Timezone").Description("Use a location such as Europe/London or America/New_York.").Value(&v.TimeZone).Validate(scheduleField("--timezone")),
		).Title("slack-hours · 3/4 · Hours").Description("The workspace stays hidden outside these hours."),
		huh.NewGroup(
			huh.NewConfirm().Title("Save this configuration?").Description(review()).DescriptionFunc(review, v).Affirmative("Save").Negative("Cancel").Value(&v.Save),
		).Title("slack-hours · 4/4 · Review").Description("Shift+Tab goes back. Ctrl+C cancels without saving."),
	).WithTheme(huh.ThemeFunc(huh.ThemeCharm))
}

func configurationWizard(ctx context.Context, input io.Reader, output io.Writer, c Config) (Config, error) {
	if ctx.Err() != nil {
		return c, errSetupCancelled
	}
	workspaces, err := chooseSlackConnection(ctx, input, output, c.Port)
	if err != nil {
		return c, err
	}
	selected := workspaces[0].ID
	for _, workspace := range workspaces {
		if workspace.ID == c.WorkspaceID {
			selected = c.WorkspaceID
		}
	}
	if c.WorkspaceID != "" && selected != c.WorkspaceID {
		replace := false
		err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Choose a different workspace?").Description(fmt.Sprintf("%s is not signed in. Cancel and sign into it in Slack, or choose a replacement.", c.WorkspaceName)).Affirmative("Choose another").Negative("Cancel").Value(&replace))).WithInput(input).WithOutput(output).RunWithContext(ctx)
		if ctx.Err() != nil || errors.Is(err, huh.ErrUserAborted) || (err == nil && !replace) {
			return c, errSetupCancelled
		}
		if err != nil {
			return c, err
		}
	}
	v := wizardValues{Workspace: selected, Name: c.WorkspaceName, Days: append([]string(nil), c.Schedule.Days...), Hours: c.Schedule.Start + "-" + c.Schedule.End, TimeZone: c.Schedule.TimeZone, Port: strconv.Itoa(c.Port)}
	err = wizardForm(&v, workspaces).WithInput(input).WithOutput(output).RunWithContext(ctx)
	if ctx.Err() != nil || errors.Is(err, huh.ErrUserAborted) {
		return c, errSetupCancelled
	}
	if err != nil {
		return c, err
	}
	if !v.Save {
		return c, errSetupCancelled
	}
	for _, workspace := range workspaces {
		if workspace.ID == v.Workspace {
			v.Name = workspace.Name
		}
	}
	return v.config()
}

func friendlyDays(days []string) string {
	names := map[string]string{"mon": "Monday", "tue": "Tuesday", "wed": "Wednesday", "thu": "Thursday", "fri": "Friday", "sat": "Saturday", "sun": "Sunday"}
	if strings.Join(days, ",") == "mon,tue,wed,thu,fri" {
		return "Monday to Friday"
	}
	out := make([]string, 0, len(days))
	for _, day := range days {
		out = append(out, names[day])
	}
	return strings.Join(out, ", ")
}
