# AGENTS.md

Guidance for coding agents working in this repository. Read it before changing
anything; the README and `docs/` describe the product, this file describes how
to work on it.

vllm-helper is a single Go binary that manages vLLM on one machine or several:
saved model profiles, a systemd/launchd service, HuggingFace search, tested
multi-machine recipes, a web panel, and a Coder model-picker sync. It is a
control plane: inference traffic goes straight to vLLM, except through a pool
router.

## Layout

| Path                           | What lives there                                                                 |
| ------------------------------ | -------------------------------------------------------------------------------- |
| `main.go`                      | Entry point; calls into `internal/cli`.                                          |
| `internal/cli`                 | Every cobra command, grouped in `root.go`.                                       |
| `internal/control`             | Operations that change or report what is serving; shared by the CLI and the web. |
| `internal/config`              | XDG directories and the layered `config.env` + `secrets.env` + profile loading.  |
| `internal/profile`             | Saved model profiles (`~/.config/vllm-helper/models/<name>.env`).                |
| `internal/service`             | The init-system unit: systemd on Linux, launchd on macOS.                        |
| `internal/detect`              | Hardware, memory budget, running vLLM processes and containers.                  |
| `internal/catalog`, `hf`       | HuggingFace search, weight sizes, flags read from model cards.                   |
| `internal/recipe`              | Tested recipes (`tested.json`, hand-authored) and container launch.              |
| `internal/nodes`, `router`     | The node registry and the session-sticky pool router.                            |
| `internal/plugin`              | Integrations (`coder`, `spark`) that contribute their own commands.              |
| `internal/web`                 | The panel served by `vllm-helper serve`; plain HTML/CSS/JS, embedded, no build.  |
| `docs/`                        | User docs, embedded in the binary and served at `/docs` and on the public site.  |
| `cmd/vllm-helper-docs`         | The public docs website, deployed from `Dockerfile.docs`.                        |
| `scripts/gendocs`              | Generates `docs/reference/cli/` and its manifest routes from the command tree.   |
| `tools/recipediff`             | A separate Go module comparing tested recipes against their upstream sources.    |
| `CHANGELOG.md`, `RELEASING.md` | Release notes and the release process.                                           |

## Checks

CI (`.github/workflows/ci.yml`) runs these; run the ones your change touches
before calling it done.

```sh
gofmt -l .                                   # must print nothing
go vet ./...
go test -race ./...
GOOS=linux  GOARCH=arm64 go build ./...      # every release target builds:
GOOS=linux  GOARCH=amd64 go build ./...      # there is build-tagged code for
GOOS=darwin GOARCH=arm64 go build ./...      # each platform
(cd tools/recipediff && go vet ./... && go test ./...)   # root ./... skips it
bun install --frozen-lockfile && bun run fmt:ci          # Prettier: Markdown, YAML, JSON
```

`bun run fmt` rewrites files in place. Prettier ignores Go files and the
fixtures listed in `.prettierignore`, which must stay byte-identical to their
sources.

## Code conventions

- Two direct dependencies (cobra, x/term) and the standard library. Don't add a
  dependency without asking; `tools/recipediff` is a separate module precisely
  so its dependencies stay out of the CLI.
- Comments explain why, not what. Most of the existing ones record a bug or a
  measurement that forced the code into its shape; keep them accurate when you
  change the code under them.
- Platform-specific code lives in `_linux.go`, `_darwin.go` and `_other.go`
  files. A function called from shared code needs a version in each, or the
  other release targets stop building.
- A new command needs a `GroupID` from `root.go`; `TestRunHelp` fails on an
  ungrouped one. Then regenerate the CLI reference (see below).
- Config, profiles and state outlive the binary. Changes to profile parsing,
  `EXTRA_ARGS` handling or the service unit must still read files written by
  the previous release; see "Upgrade safety" in `RELEASING.md`.
- Recipes in `internal/recipe/tested.json` are measured on real hardware and
  pinned by image digest. Never invent or generate one; record a deviation from
  upstream as data in the entry, not as a comment.

## Tests

- `foo_internal_test.go` is `package foo`; `foo_test.go` is `package foo_test`.
  Follow the file you're adding to.
- Tests must not touch the real machine. Point `VLLM_HELPER_CONFIG_DIR`,
  `VLLM_HELPER_STATE_DIR` and `VLLM_HELPER_DATA_DIR` at `t.TempDir()`, use
  `httptest` for HTTP, and don't call real `systemctl`, `launchctl`, `ssh` or
  `docker`. `internal/cli` routes outbound HTTP to a dead port in `TestMain`,
  so a test that needs the network fails rather than flakes.
- `internal/cli` tests swap `os.Stdout`/`os.Stderr` process-wide, so they don't
  run in parallel.
- Fix a bug with a regression test that fails without the fix.

## Docs

`docs/` is embedded with `//go:embed manifest.json *.md reference`, so every
Markdown file there ships in the binary and must be routed. Don't add stray
notes or a nested `AGENTS.md` under `docs/`.

- Every hand-written page has `title` and `description` frontmatter, kept
  identical to its entry in `docs/manifest.json`: GitHub shows the frontmatter,
  the panel and the public site show the manifest. A new page needs a manifest
  route, or `TestEveryMarkdownPageIsReachable` fails.
- Link between pages with relative `.md` paths (`./install.md`), so links work
  on GitHub, in the panel and on the public site. `TestRelativeLinksResolve`
  checks them.
- Guides are written one sentence per line, about what the user runs and sees
  rather than how the code works. Match the page you're editing.
- `docs/reference/cli/` is generated. Don't edit it; change the command's help
  text in `internal/cli` and run `go run ./scripts/gendocs` (or
  `go generate ./...`). `TestCLIReferenceIsUpToDate` fails when it is stale or
  has a page for a removed command.
- `README.md` is a short overview that points at the docs; detail goes in
  `docs/`.
- The same pages are published on the public docs site (see `Dockerfile.docs`),
  so anything under `docs/` is public: no hostnames, IPs or internal notes.

## Changelog and commits

- Add an entry under `## [Unreleased]` in `CHANGELOG.md` in the same PR as any
  user-visible change, written for someone deciding whether to upgrade. Internal
  work (refactors, tests, CI) needs none. `RELEASING.md` has the full rules.
- Commit subjects are `area: what changed`, lowercase, e.g.
  `nodes: dial a node's HTTP address from --host, not its ssh target`. The body
  explains why.
- Work on a branch and open a PR against `main`. Don't push to `main`, tag, or
  cut a release; the maintainers do that.

## Safety

- Never commit secrets: `secrets.env`, API keys, HuggingFace or Coder tokens.
  `THISBOX.md` is for machine-local notes and is gitignored; keep it that way.
- Don't run `vllm-helper setup`, `switch`, `restart`, `stop`, `recipes launch`
  or `install-service` against a real machine's config to test a change. They
  restart services and load models onto shared GPUs. Use a scratch
  `VLLM_HELPER_*_DIR` and a closed port, as the tests do, and ask before
  touching real hardware.
