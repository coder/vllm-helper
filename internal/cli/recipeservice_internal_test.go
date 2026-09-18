package cli

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

type fakeRecipeService struct {
	installed  string
	unit       string
	enabled    string
	superseded string
	failStop   bool
}

func (f *fakeRecipeService) Self() (string, error)       { return "/usr/local/bin/vllm-helper", nil }
func (f *fakeRecipeService) Home(string) (string, error) { return "/home/serve", nil }
func (f *fakeRecipeService) EnableStart(n string) error  { f.enabled = n; return nil }
func (f *fakeRecipeService) InstallSystemd(n, u string) (string, error) {
	f.installed, f.unit = n, u
	return "/etc/systemd/system/" + n, nil
}
func (f *fakeRecipeService) Supersede(n string) error {
	if f.failStop {
		return errors.New("nope")
	}
	f.superseded = n
	return nil
}

// The unit must not name a recipe: which one runs comes from the active
// profile, so switching recipes cannot mean editing the unit.
func TestRecipeUnitNamesNoRecipe(t *testing.T) {
	unit := renderRecipeSystemdUnit(recipeSystemdUnit{
		User: "serve", Home: "/home/serve", Self: "/usr/local/bin/vllm-helper", Timeout: 1800,
	})
	for _, want := range []string{
		"ExecStart=/usr/local/bin/vllm-helper restart",
		"ExecStop=/usr/local/bin/vllm-helper stop",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"TimeoutStartSec=1800",
		"User=serve",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit is missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "recipes launch") {
		t.Errorf("the unit pins a recipe, which the active profile is supposed to decide:\n%s", unit)
	}
}

// A load takes minutes, so the default systemd start timeout would kill a
// healthy launch.
func TestRecipeUnitAllowsALongLoad(t *testing.T) {
	if defaultRecipeServiceTimeout < 600 {
		t.Errorf("timeout default is %ds, too short for a large model", defaultRecipeServiceTimeout)
	}
}

func TestInstallRecipeServiceStandsDownTheNativeUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("install is implemented for systemd hosts")
	}
	cases := []struct {
		name       string
		opts       recipeServiceOptions
		superseded string
		enabled    string
	}{
		{
			name:       "supersedes the configured unit",
			opts:       recipeServiceOptions{Name: "vllm-cluster.service", User: "serve", Timeout: 60, Supersede: true, Native: "vllm.service"},
			superseded: "vllm.service",
		},
		{
			name: "leaves it alone when asked",
			opts: recipeServiceOptions{Name: "vllm-cluster.service", User: "serve", Timeout: 60, Supersede: false, Native: "vllm.service"},
		},
		{
			name: "never stands down the unit it just installed",
			opts: recipeServiceOptions{Name: "vllm.service", User: "serve", Timeout: 60, Supersede: true, Native: "vllm.service"},
		},
		{
			name:    "starts only when asked",
			opts:    recipeServiceOptions{Name: "vllm-cluster.service", User: "serve", Timeout: 60, Start: true},
			enabled: "vllm-cluster.service",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeRecipeService{}
			c.opts.Service = fake
			if err := installRecipeService(c.opts); err != nil {
				t.Fatal(err)
			}
			if fake.superseded != c.superseded {
				t.Errorf("superseded %q, want %q", fake.superseded, c.superseded)
			}
			if fake.enabled != c.enabled {
				t.Errorf("enabled %q, want %q", fake.enabled, c.enabled)
			}
			if fake.installed != c.opts.Name {
				t.Errorf("installed %q, want %q", fake.installed, c.opts.Name)
			}
		})
	}
}

// Standing the old unit down is a courtesy, not the job: failing at it must
// not lose the unit that was just installed.
func TestInstallRecipeServiceSurvivesAFailedSupersede(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("install is implemented for systemd hosts")
	}
	fake := &fakeRecipeService{failStop: true}
	err := installRecipeService(recipeServiceOptions{
		Name: "vllm-cluster.service", User: "serve", Timeout: 60,
		Supersede: true, Native: "vllm.service", Service: fake,
	})
	if err != nil {
		t.Fatalf("install failed because supersede did: %v", err)
	}
	if fake.installed == "" {
		t.Error("the unit was not installed")
	}
}
