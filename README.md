# isolate

Runs an AI coding agent ([Claude Code](https://www.npmjs.com/package/@anthropic-ai/claude-code) or
[`pi`](https://www.npmjs.com/package/@earendil-works/pi-coding-agent)) on a Mac inside a disposable
Linux VM that can see exactly one host folder.

- **One mounted folder** at `/workspace`, the only host path the agent can change.
- **Optional persistent config** for the agent (settings, login, session history), mounted from a
  directory you choose instead of your real one.
- **Optional secrets file** (e.g. `GH_TOKEN`) passed in as env vars, never read or logged by isolate.
- **Pinned toolchain**: Node, Go, git, `jq`, ssh, `gh`, and both agents with their extensions at
  locked versions.
- **Memory limit** of 4g by default.

## Requirements

- macOS on Apple silicon
- [Apple `container`](https://github.com/apple/container) (tested with 1.5.0)
- Go (version per [go.mod](go.mod)); [Task](https://taskfile.dev) and golangci-lint come in as `go tool`s
- Network access at image build time

One-time setup, repeated after each reboot: start `container` and register
`host.container.internal` so containers can reach the host (prompts for sudo).

```bash
go tool task system:dns
```

## Usage

```bash
go tool task build                    # build bin/isolate and the isolate-agents image
go tool task run -- <folder>          # run pi
go tool task run:claude -- <folder>   # run Claude Code
```

```bash
isolate run <folder> [--agent pi|claude] [--config <dir>] [--config-file <file>]
                     [--sessions <dir>] [--secrets-file <file>]... [--memory <size>] [-- <agent args>...]
```

| Flag             | Effect                                                                               |
| ---------------- | ------------------------------------------------------------------------------------ |
| `--agent`        | `pi` (default) or `claude`                                                           |
| `--config`       | Host dir mounted read-write as the agent's config dir (`/root/.pi`, `/root/.claude`) |
| `--config-file`  | Host file mounted as `~/.claude.json` (claude only; must exist)                      |
| `--sessions`     | Host dir mounted as pi's session history (pi only)                                   |
| `--secrets-file` | `key=value` file passed via `container run --env-file`; repeatable                   |
| `--memory`       | VM memory limit, default `4g`; `--memory=` for none                                  |

The folder is created if missing; if it contains a `go.mod`, dependencies are downloaded first.
`go tool task run` fills in the checked-in config paths and uses `assets/env/secrets.env` if present
(template: [secrets.env.example](assets/env/secrets.env.example)).

For Claude Code, copy `assets/agents/claude/config/claude.json.example` to `claude.json` once, then
run `/login` inside the first session; the login persists in the mounted config directory.

## Versions

- Agent packages are pinned in `assets/agents/<agent>/package.json` and its lockfile; in-container
  self-updates are disabled. Manage them with `go tool task agents:outdated`,
  `go tool task agents:bump`, and `go tool task agents:bump:all`, then `go tool task build`.
- The binary only runs the image tagged with its own version. To release, bump `version` in
  [main.go](main.go) and `IMAGE_REF` in [Taskfile.yml](Taskfile.yml) together.

## Development

```bash
go tool task test   # go test
go tool task lint   # golangci-lint
go tool task fmt    # gofmt
```

To add an agent, register it in [agent.go](agent.go) and add `assets/agents/<agent>/`
(`package.json`, lockfile, `install.sh`, `start.sh`), wired into
[assets/Containerfile](assets/Containerfile) and [assets/.dockerignore](assets/.dockerignore).
