package main

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) eugrRecipe {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := parseEugr(b)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return r
}

// The template is the hard part: line continuations, {placeholder} from
// defaults, and {{ }} escaping literal JSON. Getting any of these wrong makes
// the whole report wrong in a quiet way.
func TestEugrCommandExpandsToFlags(t *testing.T) {
	up := loadFixture(t, "deepseek-v4-flash-0731.yaml")
	flags := up.Flags()

	cases := map[string]string{
		"--tensor-parallel-size":       "2",
		"--max-num-seqs":               "8",
		"--max-model-len":              "auto",
		"--gpu-memory-utilization":     "0.85",
		"--block-size":                 "256",
		"--attention-backend":          "B12X",
		"--kv-cache-dtype":             "fp8",
		"--enable-prefix-caching":      "",
		"--max-cudagraph-capture-size": "48",
	}
	for flag, want := range cases {
		if got, ok := flags[flag]; !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", flag, got, ok, want)
		}
	}
	// {{ }} must survive as literal JSON with the placeholder filled in.
	spec := flags["--speculative-config"]
	if want := `{"method":"dspark","num_speculative_tokens":5,"draft_sample_method":"probabilistic","attention_backend":"B12X"}`; spec != want {
		t.Errorf("speculative-config =\n%s\nwant\n%s", spec, want)
	}
	if up.Model != "deepseek-ai/DeepSeek-V4-Flash-0731" {
		t.Errorf("model = %q", up.Model)
	}
	if len(up.Mods) != 1 || up.Mods[0] != "mods/instanttensor-hybrid-draft-loader" {
		t.Errorf("mods = %v; the commented-out one must not be read", up.Mods)
	}
}

// An unknown placeholder stays visible rather than becoming an empty string,
// so a report can never claim upstream passes an empty value.
func TestUnknownPlaceholderSurvives(t *testing.T) {
	r := eugrRecipe{Defaults: map[string]any{"known": 7}, Command: "vllm serve m --a {known} --b {mystery}"}
	flags := r.Flags()
	if flags["--a"] != "7" {
		t.Errorf("--a = %q, want 7", flags["--a"])
	}
	if flags["--b"] != "{mystery}" {
		t.Errorf("--b = %q, want the placeholder left alone", flags["--b"])
	}
}

func TestCompareSeparatesDeclaredFromDrift(t *testing.T) {
	up := eugrRecipe{
		Model:    "m",
		Mods:     []string{"mods/thing"},
		Defaults: map[string]any{"seqs": 8},
		Env:      map[string]string{"KEEP": "1", "CHANGED": "1", "GONE": "1"},
		Command:  "vllm serve m --max-num-seqs {seqs} --stays yes",
	}
	var e entry
	e.Model = "m"
	e.Args = []string{"--max-num-seqs", "32", "--stays", "yes", "--extra", "1"}
	e.Env = map[string]string{"KEEP": "1", "CHANGED": "2", "MINE": "1"}
	e.Source.Deviations = []struct {
		Setting  string `json:"setting"`
		Upstream string `json:"upstream"`
		Ours     string `json:"ours"`
		Why      string `json:"why"`
	}{{Setting: "max_num_seqs", Upstream: "8", Ours: "32", Why: "measured"}}

	got := map[string]finding{}
	for _, f := range compare(e, up) {
		got[f.Label] = f
	}

	if f, ok := got["--max-num-seqs"]; !ok || !f.Declared {
		t.Errorf("a declared deviation must be marked declared, got %+v", f)
	}
	if f, ok := got["--stays"]; ok {
		t.Errorf("an agreeing flag is not a finding, got %+v", f)
	}
	if f, ok := got["--extra"]; !ok || f.Declared || f.Info {
		t.Errorf("a flag only we pass is undeclared drift, got %+v", f)
	}
	if f, ok := got["mods/thing"]; !ok || f.Declared {
		t.Errorf("an unapplied mod is drift, got %+v", f)
	}
	if f, ok := got["CHANGED"]; !ok || f.Upstream != "1" || f.Ours != "2" {
		t.Errorf("a changed env value must be reported, got %+v", f)
	}
	if f, ok := got["GONE"]; !ok || f.Ours != "" {
		t.Errorf("env we dropped must be reported, got %+v", f)
	}
	if f, ok := got["MINE"]; !ok || !f.Info {
		t.Errorf("env only we set is informational plumbing, got %+v", f)
	}
	if f, ok := got["KEEP"]; ok {
		t.Errorf("matching env is not a finding, got %+v", f)
	}
	if n := undeclared(compare(e, up)); n != 4 {
		t.Errorf("undeclared = %d, want 4 (--extra, mods/thing, CHANGED, GONE)", n)
	}
}

// host and port come from the launcher, so their absence is not a decision.
func TestLauncherSuppliedFlagsAreNotDrift(t *testing.T) {
	up := eugrRecipe{Model: "m", Command: "vllm serve m --host 0.0.0.0 --port 8000"}
	var e entry
	e.Model = "m"
	if n := undeclared(compare(e, up)); n != 0 {
		t.Errorf("undeclared = %d, want 0", n)
	}
}

// A pin that trails upstream is only news when the file actually changed, so
// the comparison is on values rather than on commit ids.
func TestDiffUpstreamReportsWhatMoved(t *testing.T) {
	pinned := eugrRecipe{
		Defaults: map[string]any{"seqs": 8},
		Env:      map[string]string{"KEEP": "1", "DROPPED": "1"},
		Mods:     []string{"mods/old"},
		Command:  "vllm serve m --max-num-seqs {seqs} --gone yes",
	}
	newest := eugrRecipe{
		Defaults: map[string]any{"seqs": 16},
		Env:      map[string]string{"KEEP": "1", "ADDED": "1"},
		Mods:     []string{"mods/old", "mods/new"},
		Command:  "vllm serve m --max-num-seqs {seqs}",
	}
	got := map[string]change{}
	for _, c := range diffUpstream(pinned, newest) {
		got[c.Key] = c
	}
	if c := got["--max-num-seqs"]; c.Was != "8" || c.Now != "16" {
		t.Errorf("seqs change = %+v", c)
	}
	if c := got["--gone"]; c.Now != "(removed)" {
		t.Errorf("a dropped flag must be reported: %+v", c)
	}
	if c := got["ADDED"]; c.Was != "(absent)" {
		t.Errorf("new env must be reported: %+v", c)
	}
	if c := got["DROPPED"]; c.Now != "(removed)" {
		t.Errorf("removed env must be reported: %+v", c)
	}
	if c := got["mods/new"]; c.Now != "applied" {
		t.Errorf("a newly required mod is the whole point: %+v", c)
	}
	if _, ok := got["KEEP"]; ok {
		t.Error("unchanged env is not a change")
	}
}

func TestDiffUpstreamIsQuietWhenNothingMoved(t *testing.T) {
	r := loadFixture(t, "deepseek-v4-flash-0731.yaml")
	if c := diffUpstream(r, r); len(c) != 0 {
		t.Errorf("identical revisions reported %d changes: %+v", len(c), c)
	}
}
