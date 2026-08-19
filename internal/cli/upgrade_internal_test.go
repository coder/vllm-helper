package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/update"
)

// Upgrading has to work on a machine that has never run setup -- that is
// exactly when someone is replacing a binary they just downloaded. This runs
// after the binary is already swapped, so a crash here would report a failure
// for an upgrade that actually succeeded.
func TestRecordInstalledVersionWithoutConfig(t *testing.T) {
	dirs := newTree(t)

	if previous := recordInstalledVersion(nil, "v0.3.2"); previous != "" {
		t.Errorf("previous = %q, want empty on a machine with no record yet", previous)
	}
	got, err := os.ReadFile(dirs.InstalledVersionFile())
	if err != nil {
		t.Fatalf("nothing was recorded: %v", err)
	}
	if strings.TrimSpace(string(got)) != "v0.3.2" {
		t.Errorf("recorded %q, want v0.3.2", got)
	}
}

func TestRecordInstalledVersionReportsWhatItReplaced(t *testing.T) {
	dirs := newTree(t)
	if err := os.MkdirAll(dirs.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.InstalledVersionFile(), []byte("v0.3.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if previous := recordInstalledVersion(nil, "v0.3.2"); previous != "v0.3.1" {
		t.Errorf("previous = %q, want v0.3.1", previous)
	}
}

// The channel is remembered in config.env so it survives the upgrade that
// acts on it.
func TestResolveChannelPersistsAnExplicitChoice(t *testing.T) {
	dirs := newTree(t)
	writeConfig(t, dirs, "PORT=8001\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	ch, switched, err := resolveChannel(cfg, "dev")
	if err != nil {
		t.Fatalf("resolveChannel: %v", err)
	}
	if ch != update.Dev || !switched {
		t.Errorf("got (%q, switched=%v), want dev and a switch", ch, switched)
	}
	body, err := os.ReadFile(dirs.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "UPDATE_CHANNEL=dev") {
		t.Errorf("config.env = %q, want the channel recorded", body)
	}

	// Reloading has to see it, and asking for the same channel again is not
	// a switch -- only a real change should force a reinstall.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ch, switched, _ := resolveChannel(reloaded, "dev"); ch != update.Dev || switched {
		t.Errorf("got (%q, switched=%v), want dev with no switch", ch, switched)
	}
	if ch, switched, _ := resolveChannel(reloaded, ""); ch != update.Dev || switched {
		t.Errorf("with no flag: got (%q, switched=%v), want the remembered dev channel", ch, switched)
	}
}

// With no config there is nowhere to store a preference, but a channel given
// on the command line still has to apply to this run.
func TestResolveChannelWithoutConfig(t *testing.T) {
	newTree(t)
	if ch, switched, err := resolveChannel(nil, ""); ch != update.Stable || switched || err != nil {
		t.Errorf("got (%q, %v, %v), want stable with no switch", ch, switched, err)
	}
	if ch, switched, err := resolveChannel(nil, "dev"); ch != update.Dev || switched || err != nil {
		t.Errorf("got (%q, %v, %v), want dev with nothing persisted", ch, switched, err)
	}
	if _, _, err := resolveChannel(nil, "nightly"); err == nil {
		t.Error("an unknown channel was accepted")
	}
}

// A dev build is identified by its commit, because every dev release shares
// one moving tag and the version string sorts below the release it follows.
func TestIsCurrent(t *testing.T) {
	oldV, oldC := Version, Commit
	t.Cleanup(func() { Version, Commit = oldV, oldC })

	Version, Commit = "0.3.2-dev.abc1234", "abc1234def5678"
	// The release body carries the full sha; the binary may be stamped with
	// a prefix of it, so matching is by prefix.
	if !isCurrent(&update.Release{TagName: "dev", Body: "commit: abc1234def5678\n"}, update.Dev) {
		t.Error("the running dev build wasn't recognised as current")
	}
	if isCurrent(&update.Release{TagName: "dev", Body: "commit: 9999999999\n"}, update.Dev) {
		t.Error("a different commit was treated as current")
	}
	// Nothing to compare means we can't claim to be current: better to
	// offer the upgrade than to refuse one that's needed.
	if isCurrent(&update.Release{TagName: "dev", Body: "no commit here\n"}, update.Dev) {
		t.Error("a release with no commit was treated as current")
	}

	Version, Commit = "0.3.1", ""
	if !isCurrent(&update.Release{TagName: "v0.3.1"}, update.Stable) {
		t.Error("v0.3.1 didn't match a 0.3.1 build (the v prefix should be ignored)")
	}
	if isCurrent(&update.Release{TagName: "v0.3.2"}, update.Stable) {
		t.Error("a newer release was treated as current")
	}
}

func TestGitHubTokenPrefersConfigOverEnvironment(t *testing.T) {
	dirs := newTree(t)
	writeConfig(t, dirs, "PORT=8001\nGITHUB_TOKEN=from-config\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "from-env")
	if got := githubToken(cfg); got != "from-config" {
		t.Errorf("githubToken = %q, want the stored token to win", got)
	}
	// Without config, the environment is all there is -- and GH_TOKEN is
	// what gh(1) sets, so it's worth honouring too.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "from-gh")
	if got := githubToken(nil); got != "from-gh" {
		t.Errorf("githubToken(nil) = %q, want GH_TOKEN", got)
	}
}
