// Package vllm asks the locally installed vLLM what it supports.
//
// Model cards prescribe flags for whatever vLLM the author was running. The
// values that matter most -- tool-call and reasoning parser names -- are not
// declared in vLLM's argparse (only 36 of its ~380 flags carry choices), so
// the only authority on whether a name will be accepted is the registry
// inside the installed build. A name it doesn't know is not a warning at
// startup; it is a KeyError two seconds in, and with Restart=on-failure that
// becomes a crash loop.
//
// Everything here degrades to "unknown" rather than failing. Not being able
// to introspect vLLM must never stop a model from being pulled or served.
package vllm

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Caps is what the installed vLLM accepts.
type Caps struct {
	Version          string   `json:"version"`
	ToolParsers      []string `json:"tool_parsers"`
	ReasoningParsers []string `json:"reasoning_parsers"`
	// Flags is every option string 'vllm serve' accepts. Cards outlive the
	// flags they were written for -- --enable-reasoning was real once and
	// is now an immediate "unrecognized arguments" exit -- so a name that
	// no longer exists has to be caught alongside a value that doesn't.
	Flags []string `json:"flags"`
	// Quantizations is what the installed vLLM can actually load. Worth
	// knowing separately from the flags because it moves independently of
	// them: GGUF left vLLM core in 0.27.1 for an out-of-tree plugin, so a
	// build that accepts every flag can still be unable to read a GGUF file.
	Quantizations []string `json:"quantizations"`
}

// Known reports whether introspection actually produced a registry. An empty
// Caps means "we couldn't ask", which must be treated as "don't validate",
// never as "nothing is valid".
func (c Caps) Known() bool {
	return len(c.ToolParsers) > 0 || len(c.ReasoningParsers) > 0 || len(c.Flags) > 0
}

// KnowsQuantizations reports whether the registry was readable, so "no GGUF"
// can be told apart from "we could not ask". Refusing a pull needs certainty;
// an unreadable registry must not look like a missing feature.
func (c Caps) KnowsQuantizations() bool { return len(c.Quantizations) > 0 }

// SupportsGGUF reports whether this vLLM can load GGUF weights.
//
// In 0.27.1 GGUF moved out of vLLM into vllm-gguf-plugin, so this is false on
// a stock install and stays false until that package is added to the same
// environment. It is the difference between a model that serves and one that
// cannot start at all, which is worth knowing before a pull restarts the
// service.
func (c Caps) SupportsGGUF() bool { return contains(c.Quantizations, "gguf") }

// HasToolParser reports whether the installed vLLM registers this parser.
func (c Caps) HasToolParser(name string) bool { return contains(c.ToolParsers, name) }

// HasReasoningParser reports whether the installed vLLM registers this parser.
func (c Caps) HasReasoningParser(name string) bool { return contains(c.ReasoningParsers, name) }

// KnowsFlags reports whether the flag list was readable. Separate from
// Known() because the parser registries and the argument parser are read
// independently and either can fail on its own.
func (c Caps) KnowsFlags() bool { return len(c.Flags) > 0 }

// HasFlag reports whether 'vllm serve' accepts this option string.
func (c Caps) HasFlag(flag string) bool { return contains(c.Flags, flag) }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// introspect is the script run inside vLLM's own interpreter.
//
// The registries are populated lazily, so reading ToolParserManager's map
// directly returns {} and would silently validate nothing. list_registered()
// is the accessor that forces and returns the real set.
const introspect = `
import json
out = {"tool_parsers": [], "reasoning_parsers": [], "flags": [], "quantizations": []}
try:
    from vllm.tool_parsers import ToolParserManager
    out["tool_parsers"] = sorted(ToolParserManager.list_registered())
except Exception:
    pass
try:
    from vllm.reasoning import ReasoningParserManager
    out["reasoning_parsers"] = sorted(ReasoningParserManager.list_registered())
except Exception:
    pass
try:
    from vllm.model_executor.layers.quantization import QUANTIZATION_METHODS
    out["quantizations"] = sorted(QUANTIZATION_METHODS)
except Exception:
    pass
try:
    from vllm.entrypoints.openai.cli_args import make_arg_parser
    try:
        from vllm.utils.argparse_utils import FlexibleArgumentParser
    except Exception:
        from vllm.utils import FlexibleArgumentParser
    flags = set()
    for a in make_arg_parser(FlexibleArgumentParser())._actions:
        flags.update(a.option_strings)
    out["flags"] = sorted(flags)
except Exception:
    pass
print("VLLM_HELPER_CAPS " + json.dumps(out))
`

// capsMarker prefixes the probe's one line of real output.
//
// vLLM logs plugin registration to stdout, not stderr -- installing the GGUF
// plugin turns the script's output into two INFO lines followed by the JSON,
// and parsing the whole stream silently yielded nothing. The failure mode was
// invisible: an empty Caps means "we could not ask", so flag validation went
// quiet, the answer was never cached (making every call re-import torch), and
// the newly installed plugin was reported as missing.
const capsMarker = "VLLM_HELPER_CAPS "

// extractCaps pulls the marked line out of whatever else was printed.
func extractCaps(out []byte) ([]byte, bool) {
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), capsMarker); ok {
			return []byte(rest), true
		}
	}
	return nil, false
}

// timeout bounds the import of vLLM, which is slow (seconds) because it pulls
// in torch. Generous, since the result is cached.
const timeout = 90 * time.Second

// Probe returns what the vLLM at binPath supports, caching the answer under
// cacheDir keyed by version. A zero Caps means introspection failed, which
// callers must treat as "skip validation".
func Probe(binPath, cacheDir string) Caps {
	if binPath == "" {
		return Caps{}
	}
	version := probeVersion(binPath)
	if cached, ok := readCache(cacheDir, version); ok {
		return cached
	}
	caps := runIntrospect(binPath)
	caps.Version = version
	if caps.Known() {
		writeCache(cacheDir, version, caps)
	}
	return caps
}

// pythonFor finds the interpreter that owns a vllm executable. In a venv they
// sit side by side, and using that one rather than whatever python is on PATH
// is the difference between reading the right registry and reading none.
func pythonFor(binPath string) string {
	dir := filepath.Dir(binPath)
	for _, name := range []string{"python3", "python"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func probeVersion(binPath string) string {
	out, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		return ""
	}
	// vLLM prints a bare version; take the last field defensively in case a
	// build prefixes it.
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func runIntrospect(binPath string) Caps {
	python := pythonFor(binPath)
	if python == "" {
		return Caps{}
	}
	cmd := exec.Command(python, "-c", introspect)
	// Importing vLLM on a busy GPU box can be slow; don't hang a CLI on it.
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return Caps{}
	}
	if err != nil {
		return Caps{}
	}
	line, ok := extractCaps(out)
	if !ok {
		return Caps{}
	}
	var caps Caps
	if json.Unmarshal(line, &caps) != nil {
		return Caps{}
	}
	return caps
}
