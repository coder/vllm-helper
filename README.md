# vllm-helper

A small CLI for running a **single-model, OpenAI-compatible vLLM server** on
one machine and switching which model it serves -- built for boxes like the
NVIDIA DGX Spark / Grace-Blackwell systems (128GB unified memory) where only
one large model fits at a time, but useful on any Linux machine with a GPU.

What it gives you over raw `vllm serve`:

- **Saved model profiles** -- each model's flags (`--tool-call-parser`,
  `--reasoning-parser`, context length, ...) live in a small env file you
  write once and switch to by name.
- **One-command switching** -- `vllm-helper switch <profile>` rewrites the
  runtime config, restarts the systemd service, and tails the boot log.
- **HuggingFace search built in** -- `vllm-helper search <query>` opens an
  fzf picker with live size/license/quantization info and a "will it fit on
  this machine" check, then pulls and switches to whatever you pick.
- **Flag auto-detection** -- `vllm-helper new <org/model>` reads the model's
  HF card and pre-fills known `vllm serve` flags into the new profile.
- **Optional [Coder](https://coder.com) Agents sync** -- if configured, every
  switch keeps a Coder deployment's AI-model picker in sync so users can only
  select the model that's actually loaded.
- **Adoption of existing setups** -- already running `vllm serve` by hand or
  under your own systemd unit? `vllm-helper adopt` detects it (process scan,
  systemd, Docker/Podman, endpoint probing), saves its live command line as a
  profile, and takes over management **without restarting your server**.

## Requirements

- Linux with systemd (macOS support is planned; see Roadmap)
- An existing vLLM install (typically in a venv -- setup finds `vllm` on
  your PATH, or you set `VLLM_BIN` in config)

## Install

**Binary (recommended):** grab the latest release for your platform
(linux amd64/arm64, macOS arm64) from
[Releases](https://github.com/coder/vllm-helper/releases), put it on your
PATH, and run first-time setup:

```bash
tar xzf vllm-helper_*.tar.gz
sudo install vllm-helper /usr/local/bin/
vllm-helper setup     # detects hardware + existing vLLM, offers adoption,
                      # writes config, installs the systemd unit
```

**From source (bash scripts):** the original script implementation still
works and installs the same layout:

```bash
git clone https://github.com/coder/vllm-helper && cd vllm-helper
./install.sh          # system-scope systemd unit (needs sudo)
# or
./install.sh --user   # user-scope unit, no sudo (enables lingering)
```

Both paths are idempotent, never overwrite your config or profiles, and
never restart a running server. The layout:

| Path | What |
|---|---|
| `~/.config/vllm-helper/config.env` | Global settings: port, `VLLM_BIN`, model-size budget, optional Coder settings |
| `~/.config/vllm-helper/secrets.env` | Generated `VLLM_API_KEY` (and Coder token if used) -- chmod 600 |
| `~/.config/vllm-helper/models/*.env` | Your model profiles (seeded with curated examples) |
| `~/.local/state/vllm-helper/` | Machine state: the generated runtime env, active profile, Coder entry IDs |
| `~/.local/share/vllm-helper/` | Downloaded data (tiktoken vocab files for gpt-oss-style models) |

## Usage

```bash
vllm-helper status                  # is it running, what model, is it healthy
vllm-helper list                    # saved profiles
vllm-helper switch qwen3-coder-next # switch to a saved profile
vllm-helper new <org/model-repo>    # pull, profile, and switch to a new HF model
vllm-helper search <query>          # interactive HuggingFace search
vllm-helper remove <profile>        # delete a profile (refuses if active)
vllm-helper adopt                   # take over an already-running vLLM server
vllm-helper doctor                  # hardware, running servers, config drift
```

Point any OpenAI-compatible client at `http://<machine>:8001/v1` with the API
key from `secrets.env`. The model name to request is whatever `vllm-helper
status` shows as active.

Switching restarts the service and reloads the model -- a few minutes if the
weights are cached on disk, much longer the first time (however big the
download is). `switch`/`new`/`search` tail the boot log automatically; Ctrl+C
stops watching without stopping the server.

## Profiles

A profile is a small env file in `~/.config/vllm-helper/models/`:

```bash
# Status: TESTED AND WORKING (verified 2026-07-29)
MODEL=Qwen/Qwen3-Coder-Next-FP8
MAX_MODEL_LEN=131072
GPU_MEMORY_UTILIZATION=0.85
EXTRA_ARGS="--enable-auto-tool-choice --tool-call-parser qwen3_coder"
```

The repo ships curated examples (see `profiles/`) that were verified on
128GB Grace-Blackwell hardware, including working flag sets for
Qwen3-Coder-Next, gpt-oss-120b, and others -- each file's comment header
says what's known about it.

**Gotcha:** if `EXTRA_ARGS` has more than one flag it **must** be quoted
(`EXTRA_ARGS="--foo --bar baz"`). Unquoted, bash treats everything after the
first space as a command to run and the service fails with a confusing
`command not found`.

### Adding a brand-new model

1. **Check it fits first.** Rule of thumb: `total_params x bytes_per_param`
   must stay under the `MODEL_BUDGET_GB` in your config (the installer
   defaults it to ~70% of total memory). Bytes per param: FP8/INT8 ≈ 1,
   INT4/AWQ/GPTQ-4bit ≈ 0.5, native MXFP4 ≈ 0.5-0.6. Prefer the smallest
   *official* quantized release over raw BF16 weights.
2. `vllm-helper search <query>` (interactive, shows a live size check) or
   `vllm-helper new <org/model-repo>` if you already know the repo ID.
   Both auto-detect known `vllm serve` flags from the model card when the
   card includes a snippet -- the profile's `# Status:` line says whether
   that worked. If not, read the card and edit `EXTRA_ARGS` by hand.
3. **Verify before trusting it:** `curl localhost:8001/health` returns 200, a
   real chat completion returns populated `content`, and (if you need tools) a
   request with a `tools` array actually surfaces `message.tool_calls` --
   some models (gpt-oss) only do tool calls over `/v1/responses`.

## Coder Agents sync (optional)

If you register the machine as an `openai-compat` AI provider in a Coder
deployment, `vllm-helper` can keep Coder's model picker truthful: every
profile gets one persistent model-config entry, and only the currently
active profile is `enabled`/`is_default` -- so users literally cannot select
a model that isn't loaded. Disabled entirely unless configured.

Set in `config.env`:

```bash
CODER_URL=https://coder.example.com
CODER_AI_PROVIDER_ID=<uuid of your openai-compat provider>
```

and `CODER_API_TOKEN` in `secrets.env`. A sync failure never blocks a local
switch -- the scripts warn and continue. `vllm-helper coder-sync
<activate|register|remove> <profile>` is the manual escape hatch.

## Troubleshooting

- **Service won't start / crash loop:** `journalctl -u vllm.service -n 100
  --no-pager` (add `--user` for user scope) -- read the actual Python
  traceback, don't guess.
- **Health endpoint not responding:** probably still loading weights (5-10+
  min for a big model). Watch `journalctl -u vllm.service -f -o cat`.
- **Permission denied writing the HF cache:** something ran as root and wrote
  into `~/.cache/huggingface`. Fix ownership: `sudo chown -R $USER
  ~/.cache/huggingface`.
- **"Model architectures [...] failed to be inspected" on startup:** missing
  build tooling for a Triton kernel compile step -- install your distro's
  `python3-dev` package.
- **Empty `content` with only `reasoning` populated:** the model uses a
  structured output format that isn't parsed for chat-completions. Try
  `/v1/responses`, or check the model card for a `--reasoning-parser` flag.
- **After a reboot:** the service is enabled and comes back on its own (the
  model reloads from disk cache). If not: `systemctl status vllm.service`.

## How it's wired up

```
main.go                  Binary entry point
internal/
  cli/                   Command implementations (status, switch, adopt, doctor, setup, ...)
  config/                XDG dir resolution + bash-compatible env-file parsing
  profile/               Saved model profiles
  detect/                Detection engine: /proc scan, Docker/Podman, endpoint
                         probe, hardware (unified memory vs discrete VRAM)
  hf/                    HuggingFace Hub client + flag auto-detection
  systemd/               Service management (system and user scope)
  coder/                 Optional Coder Agents sync client

vllm-helper              Legacy bash CLI (same behavior, same config files)
install.sh               Script-based installer (see Install above)
lib/*.sh                 Bash implementation the script CLI dispatches to
profiles/                Curated example profiles, copied to your config on install
```

The Go binary and the bash scripts read and write the exact same config,
profile, and state files -- you can move between them freely.

Nothing ever writes into the checkout -- config, secrets, state, and data all
live in the XDG paths listed under Install.

## Roadmap

- **Container-managed switching**: `adopt` already detects vLLM running in
  Docker/Podman; making `switch` recreate the container is next.
- Homebrew tap and `.deb` packaging.
- macOS remote-control mode for managing a Linux vLLM box (the darwin
  binary currently makes sense for search/sizing only -- vLLM itself cannot
  serve on Apple Silicon).

## License

[Apache-2.0](LICENSE)
