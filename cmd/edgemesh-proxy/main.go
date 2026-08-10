// Command edgemesh-proxy is the EdgeMesh data-plane binary. It sits on
// the request path between services, forwarding traffic and (in later
// development phases) applying routing, health, retry, and circuit
// breaking policy.
//
// Day 3 scope: forward every request to one statically configured
// backend (connection pooling, request timeout, response handling,
// structured logging). No endpoint selection or intelligent routing —
// that arrives with the service registry and routing engine.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/buildinfo"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/logging"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/proxy"
)

const component = "edgemesh-proxy"

// shutdownGrace bounds how long in-flight requests get to finish once a
// shutdown signal arrives, before the server is closed unconditionally.
const shutdownGrace = 10 * time.Second

// readHeaderTimeout bounds how long the server waits to receive a
// client's request headers, guarding against slow-loris-style
// connections holding a listener slot open indefinitely.
const readHeaderTimeout = 5 * time.Second

// defaultConfig is this binary's component-specific baseline, layered
// under any values supplied via config file or environment.
func defaultConfig() config.Config {
	return config.Config{
		Server: config.ServerConfig{ListenAddress: "0.0.0.0:8080"},
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet(component, flag.ContinueOnError)
	configPath := fs.String("config", "", "path to a YAML configuration file (optional)")
	printVersion := fs.Bool("version", false, "print version information and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *printVersion {
		fmt.Println(buildinfo.String(component))
		return nil
	}

	cfg, err := config.Load(*configPath, defaultConfig())
	if err != nil {
		return fmt.Errorf("%s: %w", component, err)
	}

	logger := logging.New(cfg.Logging, component)

	if cfg.Upstream.Address == "" {
		return fmt.Errorf("%s: upstream.address is required (set it in -config or EDGEMESH_UPSTREAM_ADDRESS)", component)
	}
	upstreamURL, err := url.Parse(cfg.Upstream.Address)
	if err != nil {
		// config.Load already validates this, so reaching here would be
		// a bug in that validation rather than a user input error.
		return fmt.Errorf("%s: parsing already-validated upstream.address %q: %w", component, cfg.Upstream.Address, err)
	}

	handler := proxy.NewHandler(proxy.Config{
		Upstream:            upstreamURL,
		DialTimeout:         cfg.Upstream.DialTimeout.Duration,
		RequestTimeout:      cfg.Upstream.RequestTimeout.Duration,
		IdleConnTimeout:     cfg.Upstream.IdleConnTimeout.Duration,
		MaxIdleConns:        cfg.Upstream.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.Upstream.MaxIdleConnsPerHost,
	}, logger)

	srv := &http.Server{
		Addr:              cfg.Server.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       cfg.Upstream.IdleConnTimeout.Duration,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"version", buildinfo.Version,
			"node_id", cfg.Node.ID,
			"listen_address", cfg.Server.ListenAddress,
			"upstream", upstreamURL.String(),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("%s: %w", component, err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests", "grace_period", shutdownGrace.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown did not complete in time", "error", err)
			return fmt.Errorf("%s: %w", component, err)
		}
		<-serveErr // ListenAndServe has returned by now; drain the channel
	}

	logger.Info("stopped")
	return nil
}
