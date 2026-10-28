<!-- markdownlint-disable MD041 -->
<div align="center">

<h1>vllm-helper</h1>

Run one large model at a time on a GPU machine, and switch models with one command.

[Quickstart](#quickstart) | [Docs](docs/README.md) | [CLI reference](docs/reference/cli.md) | [Changelog](CHANGELOG.md)

</div>

vllm-helper manages [vLLM](https://github.com/vllm-project/vllm)'s OpenAI-compatible server on a machine. Each model's working `vllm serve` settings are saved as a profile, and switching to another profile restarts the server on it. It was built for unified-memory machines like the NVIDIA DGX Spark, where memory holds exactly one big model. It works on any Linux machine with an NVIDIA GPU, and experimentally on macOS through vLLM's CPU backend.

- Save a model's serving settings as a profile, and switch between profiles with one command
- Search HuggingFace with every result's weight size checked against this machine's memory, and vLLM flags read from its model card
- Adopt a vLLM server that is already running, without restarting it
- Control the machine from a web panel that also serves these docs
- Launch tested recipes: configurations that were run and measured on named hardware, pinned by image digest, across one or more machines
- Pool replicas behind a session-sticky router, or shard one model across machines
- Keep a [Coder](https://coder.com) deployment's model picker in step with whatever is loaded

vllm-helper is a control plane only. Inference traffic goes straight to vLLM on its own port, so a bug in vllm-helper cannot break a request in flight.

## Quickstart

> See [Install and upgrade](docs/install.md) for what setup writes and how to stay current, and [Running on macOS](docs/macos.md) for Apple Silicon.

vllm-helper manages an existing vLLM install; it does not install vLLM. Download the [latest release](https://github.com/coder/vllm-helper/releases) for Linux amd64/arm64 or macOS arm64, then install it and run setup:

```shell
tar xzf vllm-helper_*.tar.gz
sudo install vllm-helper /usr/local/bin/
vllm-helper setup
```

Setup detects the hardware and the vLLM install, generates an API key, writes config, and installs a service unit (systemd on Linux, launchd on macOS). It never overwrites existing config or restarts a running server. If `vllm` isn't on your `PATH`, set `VLLM_BIN` in `~/.config/vllm-helper/config.env`.

Pull a model and serve it, or take over a server that is already running:

```shell
vllm-helper new Qwen/Qwen3-32B   # save a profile and switch to it
vllm-helper adopt                # or: manage the vLLM server already running here
vllm-helper status               # service state, active model, endpoint health
```

Point any OpenAI-compatible client at `http://<machine>:8001/v1` with the `VLLM_API_KEY` from `~/.config/vllm-helper/secrets.env`. A first switch downloads the weights, so it takes as long as the download does.

## How it works

| Piece         | What it is                                                                    | Where it lives                                            |
| ------------- | ----------------------------------------------------------------------------- | --------------------------------------------------------- |
| Config        | Settings for every profile: port, `VLLM_BIN`, service name, Coder sync        | `~/.config/vllm-helper/config.env`                        |
| Secrets       | The endpoint's API key, and tokens for Coder and HuggingFace                  | `~/.config/vllm-helper/secrets.env`                       |
| Profile       | One model's `vllm serve` settings, as a small env file                        | `~/.config/vllm-helper/models/<name>.env`                 |
| Runner        | What serves the active profile: the native service unit, or recipe containers | `vllm.service` / `com.coder.vllm`, `vllm-cluster.service` |
| Node registry | Other machines, by name, for recipes, pools and shards                        | `~/.config/vllm-helper/nodes.json`                        |
| Endpoint      | vLLM's own OpenAI-compatible API                                              | port `8001` by default                                    |

`restart`, `stop`, `status` and `logs` act on whichever runner the active profile belongs to, and `switch` stops the outgoing runner before starting the new one, so they work the same for a native model and for a multi-machine recipe.

Everyday commands:

```shell
vllm-helper list                  # saved profiles
vllm-helper switch <profile>      # load a different model
vllm-helper search <query>        # find a model that fits this machine
vllm-helper set <profile> K=V     # change a profile setting
vllm-helper logs                  # follow the server log
vllm-helper doctor                # hardware, running servers, integrations, drift
vllm-helper serve                 # web panel, with these docs under Docs
vllm-helper recipes list          # tested configurations, and what verified them
```

`status`, `list`, `search`, `inspect`, `doctor` and `recipes list` take `--json`, and `switch`, `new` and `restart` take `--wait`, so scripts and agents can drive vllm-helper without parsing prose.

## Documentation

The docs are compiled into the binary: run `vllm-helper serve` and open the **Docs** tab. The same pages are in [`docs/`](docs/README.md):

- [**Quickstart**](docs/quickstart.md): From nothing to serving a model
- [**Profiles**](docs/profiles.md): What a profile holds, and how to edit one
- [**Models and quantizations**](docs/models.md): Finding a model that fits, and choosing a quant
- [**Memory budget**](docs/memory.md): How vllm-helper decides what this machine can hold
- [**Web panel**](docs/panel.md): The browser control plane, and the permissions it needs
- [**Coder integration**](docs/coder.md): Keep a Coder deployment's model picker truthful
- [**Recipes**](docs/recipes.md): Tested launch configurations, and how local overrides layer on top
- [**More than one machine**](docs/multi-node.md): Run recipes, pools and shards across machines
- [**Troubleshooting**](docs/troubleshooting.md): When the endpoint is down or the service crash-loops
- [**CLI reference**](docs/reference/cli.md): Every command and flag, generated from the CLI

## Support

[Open an issue](https://github.com/coder/vllm-helper/issues/new) for bugs and feature requests. Include the output of `vllm-helper doctor`, which covers the hardware, the vLLM install, running servers and config drift.

## Contributing

`go test ./...` needs no fixtures or network. CI also runs `go test -race ./...` on Linux and macOS, `go vet`, and formatting checks:

```shell
gofmt -w .                   # Go
bun install && bun run fmt   # Markdown and YAML
go run ./scripts/gendocs     # regenerate docs/reference/cli/ after changing a command or flag
```

Notable changes go in [CHANGELOG.md](CHANGELOG.md). Maintainers cutting a release follow [RELEASING.md](RELEASING.md).

## License

[Apache-2.0](LICENSE)
