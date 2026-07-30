// Package cli implements the vllm-helper commands.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/systemd"
)

// Version is stamped by goreleaser via ldflags.
var Version = "dev"

const topHelp = `vllm-helper -- manage a vLLM deployment on this machine (models, switching,
optional Coder Agents sync). Only one model is loaded at a time.

USAGE
  vllm-helper <command> [args]

COMMANDS
  status                   Quick health check: service state, active model,
                           and whether the endpoint is responding
  list                     List saved model profiles
  switch <profile>         Switch to a saved profile (restarts the server)
  new <org/model-name>     Pull a brand-new HuggingFace model, save it as a
                           profile, and switch to it
  search <query>           Search HuggingFace and pick a model to pull and
                           switch to
  remove <profile>         Delete a profile (and its Coder model entry, if
                           Coder sync is configured)
  adopt                    Detect an already-running vLLM server (host
                           process, systemd unit, or container) and save it
                           as a managed profile
  doctor                   Diagnose this machine: hardware, running servers,
                           config drift
  setup                    First-time setup: detect hardware + existing vLLM,
                           write config
  coder-sync <activate|register|remove> <profile>
                           Manually sync one profile's Coder model entry
  version                  Print the version

Run 'vllm-helper <command>' with no further arguments for per-command help.
`

// Run dispatches a command line. Returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Print(topHelp)
		return 1
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "status":
		err = cmdStatus()
	case "list":
		err = cmdList()
	case "switch":
		err = cmdSwitch(rest)
	case "new":
		err = cmdNew(rest)
	case "remove":
		err = cmdRemove(rest)
	case "search":
		err = cmdSearch(rest)
	case "start":
		err = cmdStart()
	case "coder-sync":
		err = cmdCoderSync(rest)
	case "adopt":
		err = cmdAdopt()
	case "doctor":
		err = cmdDoctor()
	case "setup":
		err = cmdSetup()
	case "version", "--version", "-v":
		fmt.Println("vllm-helper", Version)
	case "help", "--help", "-h":
		fmt.Print(topHelp)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n%s", cmd, topHelp)
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vllm-helper:", err)
		return 1
	}
	return 0
}

func loadConfig() (*config.Config, error) {
	return config.Load()
}

func service(cfg *config.Config) systemd.Service {
	return systemd.Service{Name: cfg.ServiceName(), Scope: cfg.ServiceScope()}
}

// ask prompts for a line of input with a default.
func ask(prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return def
	}
	v := strings.TrimSpace(sc.Text())
	if v == "" {
		return def
	}
	return v
}

// confirm asks a yes/no question; def is the answer for a bare Enter.
func confirm(prompt string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	v := strings.ToLower(ask(fmt.Sprintf("%s [%s]", prompt, hint), ""))
	if v == "" {
		return def
	}
	return v == "y" || v == "yes"
}
