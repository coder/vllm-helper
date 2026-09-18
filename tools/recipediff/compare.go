package main

import (
	"sort"
	"strings"
)

// entry mirrors the fields of a tested recipe this tool compares. It is a
// local copy rather than an import so the tool stays a separate module with
// its own dependency, and so a schema change here cannot break the CLI.
type entry struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Args   []string          `json:"args"`
	Env    map[string]string `json:"env"`
	Source struct {
		Repo       string `json:"repo"`
		Path       string `json:"path"`
		Commit     string `json:"commit"`
		Deviations []struct {
			Setting  string `json:"setting"`
			Upstream string `json:"upstream"`
			Ours     string `json:"ours"`
			Why      string `json:"why"`
		} `json:"deviations"`
	} `json:"source"`
}

type tested struct {
	Recipes []entry `json:"recipes"`
}

// finding is one place an entry and its upstream disagree.
type finding struct {
	Kind     string // flag, env or mod
	Setting  string // the deviation key this would need to declare
	Label    string // how it reads in the report
	Upstream string
	Ours     string
	Declared bool
	Info     bool // reported, but not a conflict
}

// compare reports every disagreement. It never resolves one: a difference we
// meant to make must be declared in source.deviations, and anything else is
// something nobody has looked at yet.
func compare(e entry, up eugrRecipe) []finding {
	declared := map[string]bool{}
	for _, d := range e.Source.Deviations {
		declared[d.Setting] = true
	}
	var out []finding
	add := func(f finding) {
		f.Declared = declared[f.Setting]
		out = append(out, f)
	}

	ours := argFlags(e.Args)
	upFlags := up.Flags()
	// The launcher supplies these, so their absence from an entry is by
	// construction rather than a decision anyone made.
	supplied := map[string]bool{"--host": true, "--port": true}

	for name, upVal := range upFlags {
		if supplied[name] {
			continue
		}
		ourVal, present := ours[name]
		switch {
		case !present:
			add(finding{Kind: "flag", Setting: settingOf(name), Label: name, Upstream: upVal, Ours: ""})
		case ourVal != upVal:
			add(finding{Kind: "flag", Setting: settingOf(name), Label: name, Upstream: upVal, Ours: ourVal})
		}
	}
	for name, ourVal := range ours {
		if _, present := upFlags[name]; !present {
			add(finding{Kind: "flag", Setting: settingOf(name), Label: name, Upstream: "", Ours: ourVal})
		}
	}

	for _, m := range up.Mods {
		add(finding{Kind: "mod", Setting: m, Label: m, Upstream: "applied", Ours: ""})
	}

	for k, upVal := range up.Env {
		ourVal, present := e.Env[k]
		switch {
		case !present:
			add(finding{Kind: "env", Setting: k, Label: k, Upstream: upVal, Ours: ""})
		case ourVal != upVal:
			add(finding{Kind: "env", Setting: k, Label: k, Upstream: upVal, Ours: ourVal})
		}
	}
	// Env we add is cluster plumbing that upstream's runner sets outside the
	// recipe, so it is reported but does not count as disagreement.
	for k, ourVal := range e.Env {
		if _, present := up.Env[k]; !present {
			add(finding{Kind: "env", Setting: k, Label: k, Upstream: "", Ours: ourVal, Info: true})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// settingOf is the deviation key for a flag: --max-num-seqs is max_num_seqs,
// which is also what upstream calls it in defaults.
func settingOf(flag string) string {
	return strings.ReplaceAll(strings.TrimPrefix(flag, "--"), "-", "_")
}

// argFlags reads our argv the same way the upstream template is read.
func argFlags(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		if name, value, ok := strings.Cut(a, "="); ok {
			out[name] = value
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			out[a] = args[i+1]
			i++
			continue
		}
		out[a] = ""
	}
	return out
}

// change is one setting that moved between two revisions of the same upstream
// file. This is the "have we fallen behind" signal: we once ran DeepSeek for a
// week without speculative decoding because nothing watched for it.
type change struct {
	Kind string
	Key  string
	Was  string
	Now  string
}

// diffUpstream reports what upstream altered between the commit we pinned and
// its newest revision of the same file.
func diffUpstream(pinned, newest eugrRecipe) []change {
	var out []change
	keys := func(m map[string]string) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	cmp := func(kind string, was, now map[string]string) {
		seen := map[string]bool{}
		for _, k := range keys(was) {
			seen[k] = true
			if n, ok := now[k]; !ok {
				out = append(out, change{kind, k, was[k], "(removed)"})
			} else if n != was[k] {
				out = append(out, change{kind, k, was[k], n})
			}
		}
		for _, k := range keys(now) {
			if !seen[k] {
				out = append(out, change{kind, k, "(absent)", now[k]})
			}
		}
	}
	cmp("flag", pinned.Flags(), newest.Flags())
	cmp("env", pinned.Env, newest.Env)

	mods := func(list []string) map[string]string {
		m := map[string]string{}
		for _, v := range list {
			m[v] = "applied"
		}
		return m
	}
	cmp("mod", mods(pinned.Mods), mods(newest.Mods))
	return out
}

// undeclared counts the findings that need a decision.
func undeclared(fs []finding) int {
	n := 0
	for _, f := range fs {
		if !f.Declared && !f.Info {
			n++
		}
	}
	return n
}
