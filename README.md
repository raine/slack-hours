# slack-hours

Want to switch off work Slack without closing your other workspaces?

slack-hours hides and mutes one workspace on your Mac outside your working
hours. Keep your communities and personal workspaces open, stay signed in to
work, and leave notification settings on your phone and other computers alone.

The hidden workspace disappears from the sidebar and native menus. Its
Cmd+number shortcut is blocked, along with desktop notifications, message and
calendar sounds, and huddle invitation sounds. Its unread counts are excluded
from Dock badge updates.

Experimental. Tested with Slack 4.52.182. Slack updates can break the hooks,
and notifications can slip through while Slack starts or reloads.

## Set up

You need macOS and Slack at `/Applications/Slack.app`. Sign into your workspaces
first. Slack needs another workspace to switch to when hiding the selected one;
with only one signed in, hiding can remain incomplete.

slack-hours controls Slack through a local debugging port. Other programs on
this Mac can use that port to access your signed-in Slack. Never forward it.
Quit Slack and reopen it normally to disable the connection.

Prebuilt releases are not available yet. Build from source with Go 1.25.8 or
later, Git, and Make:

```sh
git clone https://github.com/raine/slack-hours.git
cd slack-hours
make build
./bin/slack-hours init
```

The wizard reads your signed-in workspaces so you can choose by name. It asks
for visible days, hours, and timezone, then shows a summary before saving.
New settings default to Monday through Friday, 09:00-17:00 in your Mac's
timezone (UTC if detection fails). Existing settings keep their values.

Use arrow keys to choose a workspace, Space to toggle days, Enter to continue,
and Shift+Tab to go back. Cancel or Ctrl+C leaves configuration unchanged.
If Slack needs to restart for setup, the wizard asks you to quit it with Cmd+Q;
closing its window is not enough. It then opens Slack for you.

Start the scheduler and verify the result:

```sh
./bin/slack-hours install
"$HOME/.local/bin/slack-hours" status
```

Check for `Live guards: verified` and `Scheduler installed: true`. The workspace
may hide immediately if setup finishes outside its visible hours. If Slack is
unreachable, quit Slack, run `"$HOME/.local/bin/slack-hours" launch`, and check
status again.

To use the shorter command, add this line to your shell profile (`~/.zshrc` for
zsh), then open a new terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

**Open Slack with `slack-hours launch` from now on.** Dock, Spotlight, and
Slack's own login item can launch it without the connection this tool needs.
If that happens, quit Slack and use `slack-hours launch`. The scheduler starts
at login but does not open Slack or force it to quit.

## Set up with an agent

Paste this into a coding agent that can run commands on your Mac:

```text
Set up https://github.com/raine/slack-hours on this Mac. Read the current
README and inspect any existing installation and configuration first.
Use a published macOS release if available; otherwise build from source.
Tell me if a prerequisite is missing before installing additional tools.

Ask which workspace, working days, visible hours, and timezone I want.
Explain the local debugging connection. If Slack needs to quit, ask me
to quit it, then use slack-hours launch. Use the wizard or workspaces
--json to show actual workspace names; never guess a workspace ID.
Preserve existing settings and overrides unless I choose to change them.

Install the scheduler and verify both live Slack controls and scheduler
status. Use full executable paths if PATH is not configured. Report the
workspace, schedule, current mode, next change, and any incomplete step.
Remind me to open Slack with slack-hours launch. Do not send messages,
change account preferences, or sign out of any workspace.
```

## Daily use

```sh
slack-hours hide     # Hide until the next scheduled transition
slack-hours show     # Restore until the next scheduled transition
slack-hours auto     # Clear the override and follow the schedule
slack-hours status   # Verify the requested mode in Slack
slack-hours check    # Test local suppression when hidden
```

Showing the workspace restores its entry and unread counts without switching
to it. Hiding the selected workspace switches to another one in the sidebar,
if available.

Overrides last until the next change in scheduled visibility. For example,
`show` on Friday evening lasts until Monday morning. Use `auto` to end it
sooner. If Slack is closed or unreachable, mode changes are saved but the
command returns an error because it could not apply them. The scheduler tries
again once Slack is reachable.

## Change the schedule

```sh
slack-hours configure
```

The wizard uses your current settings as defaults. Saving keeps any manual
hide/show override; use `auto` to return to the schedule. A running scheduler
picks up edits within a minute. Changing the target workspace requires quitting
Slack and running `slack-hours launch` again.

You can also change the schedule directly:

```sh
slack-hours schedule
slack-hours schedule set --days mon-fri --hours 09:00-17:00 \
  --timezone Europe/London
```

Days can be comma-separated, such as `mon,tue,wed`. Hours must start and end on
the same day. `schedule set` clears manual overrides and applies immediately
when Slack is reachable. Timezones follow daylight saving changes.

## Release installer and updates

Once a binary release is published, the installer downloads the matching
Apple Silicon or Intel archive and verifies its SHA-256 checksum:

```sh
curl -fsSL https://raw.githubusercontent.com/raine/slack-hours/main/scripts/install -o /tmp/install-slack-hours
bash /tmp/install-slack-hours
"$HOME/.local/bin/slack-hours" init
"$HOME/.local/bin/slack-hours" install
"$HOME/.local/bin/slack-hours" status
```

The installer preserves configuration. Run it again to update, then run
`slack-hours install` to restart the scheduler with the new binary. Set
`SLACK_HOURS_VERSION` to an existing release tag to install a specific version.

For source builds, update the checkout and run `make install`. If status asks
you to reload Slack after an update, quit it and run `slack-hours launch`.

`SLACK_HOURS_PREFIX` changes the installer's standalone CLI location (default
`~/.local`). The login service uses the standard paths under `~/.local`
regardless of this installer setting.

## Troubleshooting and limits

`status` exits nonzero when Slack's controls cannot be verified. Use
`slack-hours status --json` for details.

- If Slack opened normally, quit it and run `slack-hours launch`.
- Keep one main Slack window open. Extra main windows prevent attachment.
- Sign into the target workspace in Slack before running setup. If a Slack
  update breaks workspace discovery, setup reports the error instead of
  guessing which workspace to use.
- Startup, reloads, and wake can leave gaps in suppression. A minute health
  check catches missed events and clock or configuration changes. While Slack
  is offline, reconnection retries back off to 30 seconds.
- Opening a hidden workspace through another route, such as a link, may
  briefly show it before the redirect. Some menus or popovers may still list
  it. Filtering depends on Slack's interface.
- `check` uses synthetic inputs at the local notification and sound hooks. It
  does not send a message or place a huddle call. Other audio paths may exist.
  Existing calls, audio, and previously delivered notifications remain.

Hiding changes Slack's local workspace list, which Slack may save. Sign-in
state stays intact. The tool does not change account preferences, send
messages, or patch Slack's installed files.

## Files and removal

Configuration is stored in `~/.config/slack-hours/config.json`. `init` never
overwrites it; use `configure` to make changes. The debugging port is stored
as `port` and defaults to 9338. Changing it requires quitting Slack and
running `slack-hours launch` again.

| Path | Contents |
| --- | --- |
| `~/.local/lib/slack-hours/slack-hours` | Installed binary |
| `~/.local/bin/slack-hours` | CLI symlink |
| `~/.local/state/slack-hours/override.json` | Temporary manual override |
| `~/.local/state/slack-hours/control.sock` | Private daemon socket (0600) |
| `~/.local/state/slack-hours/daemon.log` | Scheduler state changes and errors |
| `~/.local/state/slack-hours/daemon-error.log` | Service stderr |
| `~/.local/state/slack-hours/last-result.json` | Last changed result; use `status` for live results |
| `~/Library/LaunchAgents/local.slack-hours.plist` | Login service |

`SLACK_HOURS_CONFIG_DIR` and `SLACK_HOURS_STATE_DIR` override storage paths.
Use the same values for setup, installation, and commands. `install` records
those paths in the service; later shell environment changes do not update it.

```sh
slack-hours uninstall
```

This stops the scheduler, clears any override, and restores the workspace if
Slack is reachable. If restoration fails, quit and reopen Slack normally.
It retains the binary and configuration. For full removal, also delete the
CLI symlink and the `slack-hours` directories under `~/.local/lib`,
`~/.local/state`, and `~/.config`.

## Scripted setup

You can read workspace names and IDs before creating configuration:

```sh
./bin/slack-hours launch
./bin/slack-hours workspaces --json
```

Initialize using the exact ID and name from that list:

```sh
./bin/slack-hours init --workspace-id T01234567 --workspace-name 'Work' \
  --days mon-fri --hours 09:00-17:00 --timezone Europe/London
./bin/slack-hours install
```

Replace the example values with your workspace and schedule. These flags
never overwrite existing configuration.

## Development

CLI source, Go tests, and embedded hooks live in `cmd/slack-hours/`. JavaScript
hook tests live in `test/`; they require Node.js.

```sh
make check
make build
shellcheck scripts/*
actionlint
```

The optional live test changes visibility and reloads your actual Slack
client, then restores the original override:

```sh
SLACK_HOURS_LIVE=1 go test -run '^TestLiveDaemon$' -v ./cmd/slack-hours
```

Push a version tag such as `v0.1.0` to build and publish Apple Silicon and Intel
archives with SHA-256 checksums. Tests run before packaging. Both binaries and
the installer are checked on matching macOS runners before publication.
Tags such as `v0.2.0-rc.1` produce GitHub prereleases.

To build and test the same archives locally:

```sh
make release VERSION=v0.1.0
python3 test/installer.py dist v0.1.0
```

Archives go to `dist/` and include the README and dependency licenses. The
version comes from the tag; ordinary source builds report `dev`.

## License

[MIT](LICENSE).
