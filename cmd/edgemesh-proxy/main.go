// Command edgemesh-proxy is the EdgeMesh data-plane binary. It sits on
// the request path between services, forwarding traffic and (in later
// development phases) applying routing, health, retry, and circuit
// breaking policy.
//
// Day 1 scope: process bootstrap only — configuration loading, logging,
// and a graceful shutdown skeleton. No request handling exists yet.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/buildinfo"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/config"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/logging"
)

const component = "edgemesh-proxy"

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
	logger.Info("starting", "version", buildinfo.Version, "node_id", cfg.Node.ID, "listen_address", cfg.Server.ListenAddress)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The data plane itself (Day 3+) will run its listener on this
	// context and shut down when it is cancelled. For now the process
	// simply waits to be signaled so the graceful-shutdown pattern is
	// established from day one.
	<-ctx.Done()
	logger.Info("shutdown signal received, stopping")

	return nil
}
