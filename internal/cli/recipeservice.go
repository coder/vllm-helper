package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const defaultRecipeServiceName = "vllm-cluster.service"

// A big model takes minutes to load and the unit only settles once the
// endpoint answers, so systemd's default 90s start timeout would kill a
// healthy launch part-way through.
const defaultRecipeServiceTimeout = 1800

func newRecipesInstallServiceCmd() *cobra.Command {
	var (
		name      string
		user      string
		timeout   int
		now       bool
		supersede bool
	)
	c := &cobra.Command{
		Use:   "install-service",
		Short: "Install a systemd service that keeps the active recipe serving",
		Long: "Installs a unit that runs 'vllm-helper restart' at boot and 'vllm-helper stop' on\n" +
			"shutdown. The unit names no recipe: which one runs comes from the active profile,\n" +
			"so switching recipes does not mean editing the unit.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			// The unit this replaces is whichever one config names, so a
			// machine that renamed its service still has the right one stood
			// down rather than a guess.
			native := ""
			if cfg, err := loadConfig(); err == nil {
				native = cfg.ServiceName()
			}
			return installRecipeService(recipeServiceOptions{
				Name:      name,
				User:      user,
				Timeout:   timeout,
				Start:     now,
				Supersede: supersede,
				Native:    native,
				Service:   defaultRecipeServiceRunner{},
			})
		},
	}
	c.Flags().StringVar(&name, "name", defaultRecipeServiceName, "systemd unit name")
	c.Flags().StringVar(&user, "user", os.Getenv("USER"), "user the launcher runs as")
	c.Flags().IntVar(&timeout, "timeout", defaultRecipeServiceTimeout, "seconds to allow for the model to load")
	c.Flags().BoolVar(&now, "now", false, "enable and start the service immediately")
	c.Flags().BoolVar(&supersede, "supersede-native", true, "stop and disable the single-node unit this replaces")
	return c
}

type recipeServiceOptions struct {
	Name      string
	User      string
	Timeout   int
	Start     bool
	Supersede bool
	Native    string
	Service   recipeServiceRunner
}

// recipeServiceRunner is the host side, injected so the rendering and the
// supersede decision can be tested without a systemd host.
type recipeServiceRunner interface {
	Self() (string, error)
	Home(string) (string, error)
	InstallSystemd(name, unit string) (string, error)
	EnableStart(name string) error
	Supersede(name string) error
}

func installRecipeService(o recipeServiceOptions) error {
	if runtime.GOOS != "linux" {
		return errors.New("cluster services are implemented for Linux systemd hosts")
	}
	if strings.TrimSpace(o.User) == "" {
		return errors.New("--user is required")
	}
	if strings.TrimSpace(o.Name) == "" {
		return errors.New("--name is required")
	}
	if o.Timeout <= 0 {
		return errors.New("--timeout must be a number of seconds")
	}
	self, err := o.Service.Self()
	if err != nil {
		return err
	}
	home, err := o.Service.Home(o.User)
	if err != nil {
		return err
	}
	unit := renderRecipeSystemdUnit(recipeSystemdUnit{
		User:    o.User,
		Home:    home,
		Self:    self,
		Timeout: o.Timeout,
	})
	path, err := o.Service.InstallSystemd(o.Name, unit)
	if err != nil {
		return err
	}
	fmt.Printf("Installed cluster unit at %s\n", path)

	// The single-node unit cannot serve a container recipe, and left enabled
	// it races this one for the port at boot. A unit that failed months ago
	// also keeps the whole host reported as degraded.
	if o.Supersede && o.Native != "" && o.Native != o.Name {
		if err := o.Service.Supersede(o.Native); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stand down %s: %v\n", o.Native, err)
		} else {
			fmt.Printf("Stopped, disabled and cleared %s; it cannot serve a container recipe.\n", o.Native)
		}
	}
	if o.Start {
		if err := o.Service.EnableStart(o.Name); err != nil {
			return err
		}
		fmt.Printf("Enabled and started %s\n", o.Name)
		return nil
	}
	fmt.Printf("Not started. Launch a recipe first so the profile records its nodes, then: sudo systemctl enable --now %s\n", o.Name)
	return nil
}

type recipeSystemdUnit struct {
	User    string
	Home    string
	Self    string
	Timeout int
}

// renderRecipeSystemdUnit builds the unit.
//
// Type=oneshot with RemainAfterExit is right because the work is a launch
// that returns once the endpoint answers; the containers it starts outlive
// the command. ExecStop hands the same binary the teardown so both nodes are
// cleared, not just the head.
func renderRecipeSystemdUnit(u recipeSystemdUnit) string {
	return fmt.Sprintf("[Unit]\n"+
		"Description=vLLM Helper cluster (the active recipe)\n"+
		"After=network-online.target docker.service\n"+
		"Wants=network-online.target\n\n"+
		"[Service]\n"+
		"Type=oneshot\n"+
		"RemainAfterExit=yes\n"+
		"User=%s\n"+
		"Environment=HOME=%s\n"+
		"WorkingDirectory=%s\n"+
		"ExecStart=%s restart\n"+
		"ExecStop=%s stop\n"+
		"TimeoutStartSec=%d\n\n"+
		"[Install]\n"+
		"WantedBy=multi-user.target\n",
		u.User, u.Home, u.Home, u.Self, u.Self, u.Timeout)
}

type defaultRecipeServiceRunner struct{}

func (defaultRecipeServiceRunner) Self() (string, error) { return defaultPoolServiceRunner{}.Self() }

func (defaultRecipeServiceRunner) Home(user string) (string, error) {
	return defaultPoolServiceRunner{}.Home(user)
}

func (defaultRecipeServiceRunner) InstallSystemd(name, unit string) (string, error) {
	return defaultPoolServiceRunner{}.InstallSystemd(name, unit)
}

func (defaultRecipeServiceRunner) EnableStart(name string) error {
	return runPoolCommand(os.Stdout, "sudo", "systemctl", "enable", "--now", name)
}

// Supersede stands the old unit down without deleting it: native mode is
// still the only way most machines serve anything, so the unit file stays as
// the way back. reset-failed clears a stale failure, which otherwise keeps
// systemctl reporting the whole host as degraded.
func (defaultRecipeServiceRunner) Supersede(name string) error {
	_ = runPoolCommand(nil, "sudo", "systemctl", "stop", name)
	if err := runPoolCommand(nil, "sudo", "systemctl", "disable", name); err != nil {
		return err
	}
	_ = runPoolCommand(nil, "sudo", "systemctl", "reset-failed", name)
	return nil
}
