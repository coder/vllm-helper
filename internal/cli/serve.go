package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/web"
)

func newServeCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:     "serve",
		Short:   "Run a web panel for this machine",
		GroupID: groupServer,
		Long: "Serve a control panel: what model is loaded, whether it is healthy,\n" +
			"and what else could be loaded instead.\n\n" +
			"It listens on localhost unless told otherwise. The panel can restart the\n" +
			"server and change which model runs, so every request needs the token\n" +
			"printed below -- open the URL it gives you.",
		Example: "  vllm-helper serve\n" +
			"  vllm-helper serve --listen 0.0.0.0:8080",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runServe(cmd, addr) },
	}
	c.Flags().StringVar(&addr, "listen", "127.0.0.1:8080",
		"address to listen on (use 0.0.0.0:8080 to reach it from another machine)")
	return c
}

func runServe(cmd *cobra.Command, addr string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	srv, err := web.New(cfg, describeBuild())
	if err != nil {
		return err
	}

	handler := srv.Handler(web.Static())
	// A token in the query string is exchanged for a cookie on first load,
	// so it isn't repeated on every asset request.
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.SetCookie(w, r)
		handler.ServeHTTP(w, r)
	})

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	fmt.Println("vllm-helper panel")
	fmt.Println()
	fmt.Println("  ", web.ListenURL(listener.Addr().String(), srv.Token()))
	fmt.Println()
	if !web.LoopbackOnly(addr) {
		// Worth saying plainly: this is reachable from other machines, and
		// the token is the only thing between them and a stop button.
		fmt.Println("Reachable from the network. Anyone who has that URL can switch models")
		fmt.Println("and stop the server, so treat it like a password.")
		fmt.Println()
	}
	fmt.Println("Ctrl+C to stop the panel. The model server is unaffected either way.")

	httpSrv := &http.Server{
		Handler: wrapped,
		// Long enough for a systemctl restart to return, which is the
		// slowest thing any handler waits on.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// Ctrl+C should end the panel, not orphan it.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	errs := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-stop:
		fmt.Println()
		fmt.Println("Stopping the panel. The model server keeps running.")
		return httpSrv.Close()
	}
}
