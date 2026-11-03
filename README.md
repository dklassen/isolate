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

One-time setup: create the host-only network that runs use by default, then, again after each
reboot, start `container` and register `host.container.internal` so containers can reach the
host (prompts for sudo).

```bash
go tool task system:network   # once
go tool task system:dns       # after each reboot
```

## Usage

```bash
go tool task build                    # build bin/isolate and the isolate-agents image
go tool task run -- <folder>          # run pi, network locked to Ollama
go tool task run:claude -- <folder>   # run Claude Code, with internet
```

```bash
isolate run <folder> [--profile <file>] [--agent pi|claude] [--model <model>]
                     [--network ollama|host|open] [--config <dir>] [--config-file <file>]
                     [--sessions <dir>] [--firewall-log <dir>] [--secrets-file <file>]...
                     [--memory <size>] [-- <agent args>...]
```

| Flag             | Effect                                                                               |
| ---------------- | ------------------------------------------------------------------------------------ |
| `--profile`      | JSON file that sets any of the flags below; flags on the command line override it    |
| `--agent`        | `pi` (default) or `claude`                                                           |
| `--model`        | Passed to the agent as `--model`, e.g. `ollama/qwen3-coder:30b` for pi               |
| `--network`      | `ollama` (default), `host` or `open`; see [Network modes](#network-modes)            |
| `--config`       | Host dir mounted read-write as the agent's config dir (`~/.pi`, `~/.claude`)         |
| `--config-file`  | Host file mounted as `~/.claude.json` (claude only; must exist)                      |
| `--sessions`     | Host dir mounted as pi's session history (pi only)                                   |
| `--firewall-log` | Existing host dir; each run logs the `ollama` firewall's drops to its own subdir     |
| `--secrets-file` | `key=value` file passed via `container run --env-file`; repeatable                   |
| `--memory`       | VM memory limit, default `4g`; `--memory=` for none                                  |

The folder is created if missing; if it contains a `go.mod`, dependencies are downloaded first.
`go tool task run` fills in the checked-in config paths and passes no secrets. `run:claude` also
passes `assets/env/secrets.env` if present (template:
[secrets.env.example](assets/env/secrets.env.example)).

For Claude Code, copy `assets/agents/claude/config/claude.json.example` to `claude.json` once, then
run `/login` inside the first session; the login persists in the mounted config directory.

## Profiles

A profile saves one setup of agent, model, network and config, so each one can run against its
own config directory without sharing logins, sessions or extensions with the others. Keys are the
flag names, and relative paths are relative to the profile file:

```json
{
  "agent": "pi",
  "model": "ollama/qwen3-coder:30b",
  "network": "ollama",
  "config": "../agents/pi/config-ablated"
}
```

```bash
go tool task run:profile PROFILE=ablated -- <folder>   # assets/profiles/ablated.json
```

A model named in `model` must also be listed in that config's `agent/models.json` (pi).

## Network modes

| Mode     | Reaches                                                                          |
| -------- | -------------------------------------------------------------------------------- |
| `ollama` | Default. Only `host.container.internal:11434`; no internet, no other host ports  |
| `host`   | Any port on the host; no internet (`--internal` host-only network)               |
| `open`   | Internet and host (container's default network)                                  |

`ollama` and `host` use the host-only network from `go tool task system:network`. In `ollama`
mode, the entrypoint installs an iptables egress allowlist as root, then starts the agent as the
unprivileged `agent` user (see [Agent user](#agent-user)), so it can't lift it. If the firewall
can't be installed, the run stops. Nothing that needs the internet works in this mode: `go mod
download`, `gh`, web-search extensions. Pass `--network open` to allow it; `go tool task
run:claude` does, since Claude Code needs Anthropic's API.

The firewall sends dropped packets (rate-limited to 10/s) through NFLOG to `ulogd`, not the kernel
log, which writes them one JSON object per line to `/var/log/isolate/firewall.json`. Pass
`--firewall-log <dir>` to keep the log after the run: each run creates its own
`<dir>/<time>-<agent>-<suffix>/` (UTC start time, e.g. `20261103T141502Z-claude-7f3a`) and mounts
only that, so concurrent runs never share a file and can't see each other's logs. ulogd's own
errors go to `ulogd.log` beside `firewall.json`. If logging can't start, the run continues with a
warning. To query every run at once, e.g. with DuckDB:

```sql
SELECT regexp_extract(filename, '([^/]+)/firewall\.json$', 1) AS run, *
FROM read_json_auto('<dir>/*/firewall.json', filename = true);
```

## Agent user

Agents run as `agent` (uid 1001, home `/home/agent`), not root: the entrypoint does its root-only
setup, then drops to that user with no capabilities and `no_new_privs`. The agent can still write
everything mounted into the container, but not `/etc` or the firewall, and it can't `apt-get
install`; add any tool it needs to [assets/Containerfile](assets/Containerfile).

## Local and ablated models

The agent can use a model server on the host, such as Ollama at
`http://host.container.internal:11434`. Hugging Face models that have been ablated
("abliterated") or fine-tuned to remove refusals need extra care:

- **No refusals:** the model will follow destructive or exfiltrating instructions, including ones
  injected through files, web pages or tool output it reads. isolate is the only safeguard left.
- **What the agent can still reach:** the mounted folder (read-write; commit or back it up first),
  every value in `--secrets-file`, the `--config` directory, the network, and any host service
  listening on `host.container.internal`. Leave out secrets and config the session doesn't need;
  the `ablated` profile does this, with its own config and `--network ollama`. Ollama's own API
  is still reachable, so the model can delete or create models there, and `/api/pull` makes the
  host fetch from any registry URL the model names, which can leak data through that URL.
- **Model files run on the host, outside isolate:** prefer GGUF or safetensors over pickle weights
  (`.bin`, `.pt`, `.pth`), which can execute code when loaded, and never enable `trust_remote_code`
  for a repo you haven't reviewed.
- **Provenance:** check the uploader and base model; an ablated model can carry other undisclosed
  changes.

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
