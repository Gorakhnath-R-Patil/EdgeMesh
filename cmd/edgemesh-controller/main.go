// Command edgemesh-controller is the EdgeMesh control-plane binary. It
// will own service discovery, health aggregation, routing/policy
// configuration, and distribution of that configuration to proxies.
//
// Day 1 scope: process bootstrap only — configuration loading, logging,
// and a graceful shutdown skeleton. No control-plane logic exists yet.
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

const component = "edgemesh-controller"

// defaultConfig is this binary's component-specific baseline, layered
// under any values supplied via config file or environment.
func defaultConfig() config.Config {
	return config.Config{
		Server: config.ServerConfig{ListenAddress: "0.0.0.0:9090"},
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

	// The control plane's gRPC/config-distribution server (Day 15+)
	// will run on this context and shut down when it is cancelled. For
	// now the process simply waits to be signaled.
	<-ctx.Done()
	logger.Info("shutdown signal received, stopping")

	return nil
}
