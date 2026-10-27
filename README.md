# slack-hours

Hide one workspace in Slack for macOS outside your working hours. Stay signed
in, keep your other workspaces available, and leave notification settings on
other devices alone.

While hidden, the workspace disappears from the sidebar and native menus.
slack-hours blocks its numbered workspace shortcut, desktop notifications,
message and calendar sounds, and huddle-invite sound checks. It also removes
that workspace's unread counts from updates to the Dock badge.

Experimental. Tested with Slack 4.52.182. Slack updates can break the hooks,
and startup or reload gaps can let notifications through.

## Install

You need macOS, Slack at `/Applications/Slack.app`, and Go 1.25 or later to
build. The installed tool is a single binary; it needs neither Go nor Node.

Find your workspace ID in its Slack web URL: the ID follows `/client/`, for
example `https://app.slack.com/client/T01234567/...`. Use the workspace name
exactly as Slack displays it.

```sh
make build
./bin/slack-hours init --workspace-id T01234567 --workspace-name 'Work'
./bin/slack-hours install
```

Replace the example ID and name with yours. Add `~/.local/bin` to your `PATH`.
Then quit Slack and start it with:

```sh
slack-hours launch
```

Slack must be launched this way to enable its local debugging connection.
Ordinary launches do not enable it. The tool never forces Slack to quit, and
the scheduler leaves Slack closed when you close it.

`install` copies the binary to `~/.local/lib/slack-hours/slack-hours` and starts
a login service. It runs independently of the source checkout. To update it,
run `make install`. If status asks you to reload Slack after an update, quit
Slack and run `slack-hours launch` again.

## Daily use

```sh
slack-hours hide     # Hide until the next scheduled transition
slack-hours show     # Restore until the next scheduled transition
slack-hours auto     # Clear the override and follow the schedule
slack-hours status   # Verify the requested mode in Slack
slack-hours check    # Apply the mode and test local suppression when hidden
```

Showing the workspace restores its entry and unread counts without switching
to it. Hiding the selected workspace switches to another workspace in the sidebar,
if one is available. Without an alternative, the workspace can remain open
and the command reports that hiding is incomplete.

The default schedule shows the workspace Monday through Friday, 06:00 to
18:00 in Europe/Helsinki, including daylight saving changes. It stays hidden
at other times.

```sh
slack-hours schedule
slack-hours schedule set --days mon-fri --hours 09:00-17:00 \
  --timezone Europe/London
```

Days can also be comma-separated, such as `mon,tue,wed`. Hours must start and
end on the same day. Schedule changes clear manual overrides and apply
immediately when Slack is reachable.

Overrides last until the next change in scheduled visibility. For example,
`show` on Friday evening lasts until Monday morning. Use `auto` to end it
sooner. If Slack is offline, commands save the requested mode but return an
error because they could not apply it. The scheduler retries automatically.

## Troubleshooting and limits

Run `slack-hours status` to check whether Slack's visibility and notification
controls are installed and match the requested mode. The command exits nonzero
if verification fails. Add `--json` for detailed results. If debugging is
unavailable, quit Slack and run `slack-hours launch`.

- Startup, reloads, and wake can leave gaps in suppression. Schedule boundaries
  have timers; a minute health check catches missed events and clock or config
  changes. Reconnection retries back off to 30 seconds while Slack is offline.
- `check` tests synthetic inputs at the local notification and sound hooks.
  It does not send a real message or place a huddle call. Other audio paths may
  exist. Existing calls, audio, and previously delivered notifications remain.
- Hidden workspace selections outside the numbered shortcut path fall back to
  a redirect and may briefly appear. Menu filtering depends on Slack's markup;
  not every popover or deep link is covered. Multiple main client windows are
  rejected.
- The debugging port (9338 by default) listens on loopback, but other local processes can use it
  to access your signed-in Slack. Never forward or expose it. Quit Slack and
  reopen it normally to disable debugging.

The tool changes local presentation metadata, which Slack may persist. It
keeps sign-in state intact and does not change account preferences, send
messages, or patch Slack's installed files. Runtime hooks disappear on reload
or restart; the scheduler reinstalls them while debugging is available.

## Configuration and removal

`init` creates configuration only if it does not exist; rerunning it with
different workspace flags leaves existing settings unchanged. To change the
workspace, edit `workspaceId` and `workspaceName` in
`~/.config/slack-hours/config.json`. The debugging port is stored as `port`.

After changing the workspace or port, quit Slack and run `slack-hours launch`
again. Schedule commands update the same file without requiring a restart.

Other files:

| Path | Contents |
| --- | --- |
| `~/.local/state/slack-hours/override.json` | Temporary manual override |
| `~/.local/state/slack-hours/control.sock` | Private daemon socket (0600) |
| `~/.local/state/slack-hours/daemon.log` | Scheduler state changes and errors |
| `~/.local/state/slack-hours/daemon-error.log` | Service stderr |
| `~/.local/state/slack-hours/last-result.json` | Last changed result; use `status` for a live check |
| `~/Library/LaunchAgents/local.slack-hours.plist` | Login service |

`SLACK_HOURS_CONFIG_DIR` and `SLACK_HOURS_STATE_DIR` override the configuration
and state directories. Use the same values when initializing, installing and
running commands. `install` records those paths in the login service; later
changes to your shell environment do not update that service.

```sh
slack-hours uninstall
```

This stops the scheduler and restores the workspace if Slack is reachable.
It retains the binary and configuration. If restoration fails, quit and
reopen Slack normally; Slack rebuilds its workspace list from the web client.

## Development

```sh
make check   # Go vet, race-enabled tests, JavaScript hook tests
make build
```

Node.js is required for the hook tests. The runtime uses Go,
`gorilla/websocket`, and embedded JavaScript hooks. The daemon keeps one
connection to Slack, with timers and renderer events driving reconciliation.
A filtered DOM observer maintains hidden entries and disconnects while shown.

The optional live test changes workspace visibility and reloads your actual
Slack client. It restores the original override afterward:

```sh
SLACK_HOURS_LIVE=1 go test -run '^TestLiveDaemon$' -v .
```

## License

[MIT](LICENSE).
