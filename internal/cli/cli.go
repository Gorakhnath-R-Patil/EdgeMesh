// Package cli provides a minimal subcommand dispatcher shared by
// EdgeMesh's command-line tooling. It intentionally does not depend on
// any third-party CLI framework: EdgeMesh's command surface starts small
// (Day 1 registers only "version" and "help") and is expected to grow
// deliberately, one explicit Command at a time.
package cli

import (
	"fmt"
	"io"
	"sort"
)

// Command is a single named subcommand.
type Command struct {
	// Name is the token typed after the binary name, e.g. "version".
	Name string
	// Short is a one-line description shown in `help` output.
	Short string
	// Run executes the command with the remaining, unparsed arguments.
	Run func(args []string) error
}

// App is a set of registered commands plus the output stream usage text
// is written to.
type App struct {
	Name     string
	Output   io.Writer
	commands map[string]Command
}

// NewApp creates an App for a binary named name (used in usage output),
// writing help/usage text to out.
func NewApp(name string, out io.Writer) *App {
	return &App{
		Name:     name,
		Output:   out,
		commands: make(map[string]Command),
	}
}

// Register adds cmd to the app. Registering two commands with the same
// Name panics: that is a programming error caught at startup, not a
// runtime condition to handle gracefully.
func (a *App) Register(cmd Command) {
	if cmd.Name == "" {
		panic("cli: command registered with empty Name")
	}
	if _, exists := a.commands[cmd.Name]; exists {
		panic(fmt.Sprintf("cli: command %q registered more than once", cmd.Name))
	}
	a.commands[cmd.Name] = cmd
}

// Run dispatches args[0] to the matching registered command, passing it
// args[1:]. It returns an error if no command name was given or the name
// does not match any registered command; in both cases usage has already
// been written to a.Output.
func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.usage()
		return fmt.Errorf("%s: no command given", a.Name)
	}

	name := args[0]
	cmd, ok := a.commands[name]
	if !ok {
		fmt.Fprintf(a.Output, "%s: unknown command %q\n\n", a.Name, name)
		a.usage()
		return fmt.Errorf("%s: unknown command %q", a.Name, name)
	}

	return cmd.Run(args[1:])
}

// Usage writes a sorted list of registered commands to a.Output. It is
// exported so a "help" command can invoke the same output Run() falls
// back to on missing/unknown commands.
func (a *App) Usage() {
	a.usage()
}

// usage writes a sorted list of registered commands to a.Output.
func (a *App) usage() {
	fmt.Fprintf(a.Output, "Usage: %s <command> [arguments]\n\nCommands:\n", a.Name)

	names := make([]string, 0, len(a.commands))
	for name := range a.commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Fprintf(a.Output, "  %-12s %s\n", name, a.commands[name].Short)
	}
}
