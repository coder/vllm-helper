# vllm-helper

Run one large model at a time on a single GPU machine, and switch models with one command.

vllm-helper wraps [vLLM](https://github.com/vllm-project/vllm)'s OpenAI-compatible server with saved model profiles, HuggingFace search, and optional [Coder](https://coder.com) Agents sync. It was built for unified-memory machines like the NVIDIA DGX Spark, where memory fits exactly one big model, but it works on any Linux machine with an NVIDIA GPU.

## Install

Download the latest [release](https://github.com/coder/vllm-helper/releases) for your platform (Linux amd64/arm64, macOS arm64) and run setup:

```bash
tar xzf vllm-helper_*.tar.gz
sudo install vllm-helper /usr/local/bin/
vllm-helper setup
```

Setup detects your hardware and any existing vLLM install, generates an API key, writes config, and installs a service unit — a systemd unit on Linux, a launchd job on macOS — in system or user scope. It never overwrites existing config and never restarts a running server.

Requires a vLLM install (usually in a venv). If `vllm` isn't on your PATH, set `VLLM_BIN` in `~/.config/vllm-helper/config.env`.

### Updating

```bash
vllm-helper upgrade --check          # what's available, without installing
vllm-helper upgrade                  # replace this binary
vllm-helper upgrade --channel dev    # follow main instead of releases
```

There are two channels. `stable` is the tagged releases. `dev` is rebuilt from `main` on every commit that passes CI, so it carries work that hasn't been released yet — useful for trying a fix before it ships. The channel you pass is remembered, so later `vllm-helper upgrade` runs stay on it; switch back with `--channel stable`.

Upgrading replaces the binary only. A running server was started by the old one and keeps serving until `vllm-helper restart`. If the binary lives somewhere only root can write, run the upgrade with `sudo`. While this repository is private, reading releases needs a token: set `GITHUB_TOKEN` in the environment or in `config.env`.

| Platform               | Service manager | vLLM backend                             |
| ---------------------- | --------------- | ---------------------------------------- |
| Linux + NVIDIA GPU     | systemd         | CUDA — the setup this tool was built for |
| macOS on Apple Silicon | launchd         | CPU only, experimental (see below)       |

## Already running vLLM?

```bash
vllm-helper adopt
```

Adopt finds your running server (host process, systemd unit, or Docker/Podman container), saves its live command line as a profile, and takes over future switches. It does not restart or interrupt anything.

## Usage

```bash
vllm-helper status              # service state, active model, endpoint health
vllm-helper list                # saved profiles
vllm-helper switch <profile>    # load a different model (restarts the server)
vllm-helper new <org/model>     # pull a HuggingFace model and switch to it
vllm-helper search <query>      # find models on HuggingFace, with size checks
vllm-helper inspect <org/model> # license, size vs budget, detected vLLM flags
vllm-helper edit <profile>      # open a profile in $EDITOR (validated on save)
vllm-helper set <profile> K=V   # change a profile setting from the CLI
vllm-helper remove <profile>    # delete a profile
vllm-helper restart             # re-apply the active profile and restart
vllm-helper stop                # stop the server
vllm-helper logs                # follow the server journal
vllm-helper doctor              # hardware, running servers, integrations, drift
vllm-helper serve               # web panel for this machine
```

### Web panel

```bash
vllm-helper serve                          # localhost only
vllm-helper serve --listen 0.0.0.0:8080    # reachable from another machine
```

Shows what's loaded, how much of the machine's memory is in use, and what else you could switch to, with the server's log underneath. It's a control plane only — inference traffic goes straight to vLLM on its own port, so the panel is never in the path of a request.

Every request needs the token printed at startup; open the URL it gives you and it becomes a cookie. Treat that URL like a password, especially with `--listen`: anyone who has it can switch models and stop the server.

Switching and stopping need permission to control the service. On Linux the unit is system-scope by default, so either run the panel with `sudo`, or allow those three commands for your user:

```
youruser ALL=(root) NOPASSWD: /usr/bin/systemctl restart vllm.service, /usr/bin/systemctl stop vllm.service, /usr/bin/systemctl start vllm.service
```

Without it the panel still reports everything, and says why the buttons are disabled rather than failing when you press them.

Point any OpenAI-compatible client at `http://<machine>:8001/v1` with the key from `~/.config/vllm-helper/secrets.env`. Switching reloads the model, which takes a few minutes when the weights are cached and longer on first download. `switch` waits until the endpoint responds; if the service fails or crash-loops, it exits with the last log lines.

`new` and `search` read the model's HuggingFace card and pre-fill known vLLM serving flags (tool call parsers, reasoning parsers, context length). `search` also checks each model's size against what your machine can actually hold.

### Scripting

`status`, `list`, `search`, `inspect`, and `doctor` take `--json`. `switch`, `new`, and `restart` take `--wait` (block until the endpoint is healthy), `--no-follow` (return immediately), and `--timeout`. When stdout is piped, commands don't tail logs or start pickers; when stdin isn't a terminal, prompts take their defaults and sudo runs with `-n`.

## Profiles

A profile is a small env file in `~/.config/vllm-helper/models/`:

```bash
# Status: verified 2026-07-29
MODEL=Qwen/Qwen3-Coder-Next-FP8
MAX_MODEL_LEN=131072
GPU_MEMORY_UTILIZATION=0.85
EXTRA_ARGS="--enable-auto-tool-choice --tool-call-parser qwen3_coder"
```

Keep `EXTRA_ARGS` quoted if it has more than one flag; flag values with spaces (like JSON) can be quoted inside it. Before pulling a large model, prefer the smallest official quantized release: roughly 1 byte per parameter for FP8/INT8 and 0.5 for INT4/AWQ/GPTQ, against the budget `doctor` reports for your machine.

## macOS

vLLM has no GPU backend on Apple Silicon, so a Mac runs models on the CPU through vLLM's experimental CPU backend. Expect it to be useful for small models and development, not for serving anything large.

There are no Apple Silicon wheels, so vLLM has to be built from source:

```bash
brew install cmake ninja
uv venv --python 3.12 ~/venvs/vllm
git clone https://github.com/vllm-project/vllm.git ~/vllm-src && cd ~/vllm-src
VIRTUAL_ENV=~/venvs/vllm uv pip install -r requirements/cpu.txt --index-strategy unsafe-best-match
VIRTUAL_ENV=~/venvs/vllm VLLM_TARGET_DEVICE=cpu uv pip install -e . --index-strategy unsafe-best-match
```

Then `vllm-helper setup` and point `VLLM_BIN` at `~/venvs/vllm/bin/vllm`. Three differences from a GPU box are worth knowing:

- **Quantized weights don't work.** The CPU backend handles FP32, FP16 and BF16, so the usual advice to reach for an FP8 or INT4 release doesn't apply — pick a model small enough to run unquantized.
- **`GPU_MEMORY_UTILIZATION` means system RAM.** Despite the name, on the CPU backend it reserves a fraction of total RAM and vLLM refuses to start unless that much is free right then. Profiles default to a much lower fraction here, and `start` lowers it further when the machine is busy, logging the change.
- **Logs are a file, not a journal.** The launchd job writes to `~/Library/Logs/vllm-helper/<label>.log`.

A 0.6B model in BF16 serves at roughly 30 tokens/sec on an M4 Pro, which is enough to develop against.

## Coder Agents sync

If this machine backs an `openai-compat` AI provider in a Coder deployment, vllm-helper can keep Coder's model picker truthful: every profile gets a model-config entry, and only the loaded model is enabled. Set `CODER_URL` and `CODER_AI_PROVIDER_ID` in `config.env` and `CODER_API_TOKEN` in `secrets.env`. Sync failures never block a local switch.

`vllm-helper coder-sync register-all` adds a disabled entry for any profile that doesn't have one yet; existing entries are left as-is. `vllm-helper doctor` verifies the configured URL and token against the API.

## Troubleshooting

Run `vllm-helper doctor` first. It checks the build toolchain vLLM needs, inventories running servers, and warns when config has drifted from reality. For crash loops, read the actual traceback: `journalctl -u vllm.service -n 100` on Linux, or `tail -n 100 ~/Library/Logs/vllm-helper/com.coder.vllm.log` on macOS. `vllm-helper status` prints the right command for your platform when the endpoint isn't responding, and says whether the service is still loading or failing to start — a large model can take many minutes to load, so the two look identical from the outside.

A first `switch` or `new` downloads the weights, which can keep the boot log on screen for a long time. Ctrl+C there only stops watching; the server keeps loading in the background, and `vllm-helper status` will tell you when it's up. Use `--no-follow` to skip the wait entirely.

## Development

`go test ./...` needs no fixtures or network — it spawns real processes and local servers. CI additionally runs `go test -race ./...` on Linux and macOS, since the service, process-scanning, and hardware code is build-tagged per platform.

Formatting is checked in CI and is worth running before pushing:

```sh
gofmt -w .                 # Go
bun install && bun run fmt # Markdown and YAML
```

## Changelog

Notable changes are in [CHANGELOG.md](CHANGELOG.md). Maintainers cutting a release should follow [RELEASING.md](RELEASING.md).

## License

[Apache-2.0](LICENSE)
