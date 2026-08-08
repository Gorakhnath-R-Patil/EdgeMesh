// Command edgemesh-cli is the operator-facing command-line tool for
// interacting with an EdgeMesh deployment.
//
// Day 1 scope: the subcommand dispatch foundation only, with "version"
// and "help" as the sole commands. Commands for inspecting service
// registry state, routing decisions, and configuration are added as
// those subsystems are built.
package main

import (
	"fmt"
	"os"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/buildinfo"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/cli"
)

const component = "edgemesh-cli"

func main() {
	app := cli.NewApp(component, os.Stdout)

	app.Register(cli.Command{
		Name:  "version",
		Short: "Print edgemesh-cli version information",
		Run: func(args []string) error {
			fmt.Println(buildinfo.String(component))
			return nil
		},
	})

	app.Register(cli.Command{
		Name:  "help",
		Short: "Show available commands",
		Run: func(args []string) error {
			app.Usage()
			return nil
		},
	})

	if err := app.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
