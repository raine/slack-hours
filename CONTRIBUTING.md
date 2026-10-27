# Development

To build from source, install Go 1.25.8 or later, Git, and Make:

```sh
git clone https://github.com/raine/slack-hours.git
cd slack-hours
make build
./bin/slack-hours init
./bin/slack-hours install
```

For source updates, pull the latest code and run `make install`.

Go source, tests, and embedded hooks live in `cmd/slack-hours/`. JavaScript
hook tests live in `test/` and require Node.js.

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

`slack-hours check` tests synthetic inputs at the local notification and
sound hooks. It does not send messages or place huddle calls.

## Releases

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

The installer accepts `SLACK_HOURS_VERSION` to select a release and
`SLACK_HOURS_PREFIX` to change its standalone CLI location. The login service
always installs under `~/.local`.
