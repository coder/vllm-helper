<!-- markdownlint-disable MD041 -->
<div align="center">

<h1>vllm-helper</h1>

Serve large models with vLLM on one GPU machine or several, and switch models with one command.

[Quickstart](#quickstart) | [Docs][docs] | [CLI reference][docs-cli-reference] | [Changelog](CHANGELOG.md)

</div>

vllm-helper manages [vLLM](https://github.com/vllm-project/vllm)'s OpenAI-compatible server on one machine or across several. Each model's working settings are saved as a profile, and switching to another profile restarts the server on it, whether that model runs on this machine alone or spans a group of them. It was built for unified-memory machines like the NVIDIA DGX Spark, where one machine holds one big model and a bigger model needs two or more working together. It works on any Linux machine with an NVIDIA GPU, and experimentally on macOS through vLLM's CPU backend.

- Save a model's serving settings as a profile, and switch between profiles with one command
- Search HuggingFace with every result's weight size checked against the machine's memory, and vLLM flags read from its model card
- Adopt a vLLM server that is already running, without restarting it
- Register other machines once, then launch tested recipes across them: configurations run and measured on named hardware, pinned by image digest
- Pool replicas of one model behind a session-sticky router, or shard a model too big for one machine across several
- Control a machine from a web panel that also serves these docs
- Keep a [Coder](https://coder.com) deployment's model picker in step with whatever is loaded

> [!NOTE]
> vllm-helper is experimental. It is a useful way to launch local LLMs, but use it on dedicated hardware that runs only your models, on a safe network.
>
> A bug in vllm-helper can affect what it controls: the service that setup installs, the vLLM restart when you switch profiles, the web panel, and the model list that it syncs to Coder. A bug cannot break a request in flight, because vllm-helper is a control plane and inference traffic goes straight to vLLM on its own port. The one exception is a pool, because its router sits in the request path.

## Quickstart

> See [Install and upgrade][docs-install] for what setup writes and how to stay current, and [Running on macOS][docs-macos] for Apple Silicon.

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

To serve across machines, register every machine a recipe will use on the one that will serve the API, that one included, then launch. vllm-helper drives the other nodes over SSH and runs the recipe's container on each with Docker:

```shell
vllm-helper node add spark1 --host http://spark1:8001 --ssh coder@spark1   # this machine
vllm-helper node add spark2 --host http://spark2:8001 --ssh coder@spark2
vllm-helper recipes list                              # what is tested, and on what hardware
vllm-helper recipes launch <id> --nodes spark1,spark2
```

[More than one machine][docs-multi-node] covers recipes, pools and shards.

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

The docs are compiled into the binary: run `vllm-helper serve` and open the **Docs** tab. The same pages are on the [docs site][docs], and their Markdown source is in [`docs/`](docs/README.md):

- [**Quickstart**][docs-quickstart]: From nothing to serving a model
- [**Profiles**][docs-profiles]: What a profile holds, and how to edit one
- [**Models and quantizations**][docs-models]: Finding a model that fits, and choosing a quant
- [**Memory budget**][docs-memory]: How vllm-helper decides what this machine can hold
- [**Web panel**][docs-panel]: The browser control plane, and the permissions it needs
- [**Coder integration**][docs-coder]: Keep a Coder deployment's model picker truthful
- [**Recipes**][docs-recipes]: Tested launch configurations, and how local overrides layer on top
- [**More than one machine**][docs-multi-node]: Run recipes, pools and shards across machines
- [**Troubleshooting**][docs-troubleshooting]: When the endpoint is down or the service crash-loops
- [**CLI reference**][docs-cli-reference]: Every command and flag, generated from the CLI

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

<!--
  TEMPORARY DOMAIN: vllm-helper-docs.up.railway.app is the Railway-generated
  domain for the public docs site (Dockerfile.docs), in use until it has a
  permanent hostname. When that changes, update these links and the BASE_URL
  variable on the Railway docs service.
-->

[docs]: https://vllm-helper-docs.up.railway.app/docs
[docs-cli-reference]: https://vllm-helper-docs.up.railway.app/docs/reference/cli
[docs-install]: https://vllm-helper-docs.up.railway.app/docs/install
[docs-macos]: https://vllm-helper-docs.up.railway.app/docs/macos
[docs-multi-node]: https://vllm-helper-docs.up.railway.app/docs/multi-node
[docs-quickstart]: https://vllm-helper-docs.up.railway.app/docs/quickstart
[docs-profiles]: https://vllm-helper-docs.up.railway.app/docs/profiles
[docs-models]: https://vllm-helper-docs.up.railway.app/docs/models
[docs-memory]: https://vllm-helper-docs.up.railway.app/docs/memory
[docs-panel]: https://vllm-helper-docs.up.railway.app/docs/panel
[docs-coder]: https://vllm-helper-docs.up.railway.app/docs/coder
[docs-recipes]: https://vllm-helper-docs.up.railway.app/docs/recipes
[docs-troubleshooting]: https://vllm-helper-docs.up.railway.app/docs/troubleshooting
