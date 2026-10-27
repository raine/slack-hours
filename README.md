# slack-hours

Want to switch off work Slack without closing your other workspaces?

slack-hours hides and mutes one workspace on your Mac outside working hours.
Keep your communities and personal workspaces open without signing out of
work or changing notification settings on your other devices.

## Set up

You need macOS, Slack in `/Applications`, and at least two signed-in
workspaces so Slack has somewhere to switch when work is hidden.

Install the binary, then run setup:

```sh
curl -fsSL https://raw.githubusercontent.com/raine/slack-hours/main/scripts/install -o /tmp/install-slack-hours
bash /tmp/install-slack-hours
"$HOME/.local/bin/slack-hours" init
```

Pick your work workspace by name, choose its working days and hours, and
check the timezone. The wizard guides you through connecting to Slack and
asks before saving.

Then start the scheduler:

```sh
"$HOME/.local/bin/slack-hours" install
"$HOME/.local/bin/slack-hours" status
```

Look for `Live guards: verified` and `Scheduler installed: true`. Your work
workspace may hide immediately if you're outside its scheduled hours.

Add this line to `~/.zshrc` and open a new terminal to use the shorter command:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

**Open Slack with `slack-hours launch` from now on.** Opening it from the Dock
or Slack's own login item doesn't enable the connection this tool needs.
If that happens, quit Slack with Cmd+Q and run `slack-hours launch`.
The scheduler starts at login but leaves Slack closed until you open it.

## Let an agent set it up

Paste this into a coding agent that can run commands on your Mac:

```text
Set up https://github.com/raine/slack-hours on this Mac. Read its README
and check for an existing installation first. Use a release if available,
otherwise build from source; ask before installing missing prerequisites.

Ask which workspace, working days, hours, and timezone I want. Choose
from Slack's actual workspace list using the wizard or workspaces --json;
never guess an ID. Preserve existing settings unless I ask to change them.
Explain the local debugging connection and ask me to quit Slack if needed.

Install the scheduler, verify both Slack controls and scheduler status,
and report the schedule, current mode, and next change. Remind me to open
Slack with slack-hours launch. Don't send messages or change my accounts.
```

## Daily use

```sh
slack-hours hide        # Hide work for now
slack-hours show        # Bring it back for now
slack-hours auto        # Return to the schedule
slack-hours configure   # Change workspace or working hours
slack-hours status      # Check whether it's working
```

`hide` and `show` last until the next scheduled change. For example, showing
work on Friday evening lasts until Monday morning. `auto` ends the override
sooner. Showing a workspace makes it available without switching to it.
If Slack is unreachable, changes are saved despite the error and apply when
you next run `slack-hours launch`.

Changing settings keeps any active override. Run `auto` to follow the new
schedule. If you choose a different workspace, quit Slack and run
`slack-hours launch` again.

## Things to know

- **Experimental:** tested with Slack 4.52.182. Slack updates can break it,
  and notifications can slip through during startup, reloads, or wake.
- Work is hidden from the sidebar and Mac's menus, and its Cmd+number shortcut
  is blocked. Other routes, such as links, may briefly show it. Existing calls,
  audio, and already-delivered notifications aren't removed.
- Slack runs with a local debugging port that other programs on your Mac can
  use to access your signed-in Slack. Never expose that port. Quit and reopen
  Slack normally to turn it off; scheduling won't work until you launch it
  through slack-hours again.

If it isn't working, keep just one main Slack window open and run
`slack-hours status`. The usual fix is to quit Slack with Cmd+Q, then run
`slack-hours launch`. Use `status --json` for more detail.

## Update or remove

Run the installer again, then `slack-hours install` and `slack-hours status`
to restart the scheduler and check the update. Your settings are preserved.

To stop scheduling and restore the workspace:

```sh
slack-hours uninstall
```

If Slack isn't reachable, quit and reopen it normally. Uninstall keeps the
binary and settings. To remove everything, also delete `~/.local/bin/slack-hours`
and the `slack-hours` directories in `~/.local/lib`, `~/.local/state`, and
`~/.config`.

[Development and releases](CONTRIBUTING.md) · [MIT license](LICENSE)
