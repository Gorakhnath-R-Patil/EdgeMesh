package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/cli"
)

func TestRunDispatchesToMatchingCommand(t *testing.T) {
	var buf bytes.Buffer
	app := cli.NewApp("edgemesh-cli", &buf)

	var gotArgs []string
	app.Register(cli.Command{
		Name:  "version",
		Short: "print version",
		Run: func(args []string) error {
			gotArgs = args
			return nil
		},
	})

	if err := app.Run([]string{"version", "--extra"}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--extra" {
		t.Errorf("Run() passed args = %v, want [--extra]", gotArgs)
	}
}

func TestRunWithNoArgsWritesUsageAndErrors(t *testing.T) {
	var buf bytes.Buffer
	app := cli.NewApp("edgemesh-cli", &buf)
	app.Register(cli.Command{Name: "help", Short: "show help", Run: func([]string) error { return nil }})

	err := app.Run(nil)
	if err == nil {
		t.Fatal("Run() error = nil, want error for missing command")
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("Run() output = %q, want it to contain usage text", buf.String())
	}
}

func TestRunWithUnknownCommandWritesUsageAndErrors(t *testing.T) {
	var buf bytes.Buffer
	app := cli.NewApp("edgemesh-cli", &buf)
	app.Register(cli.Command{Name: "version", Short: "print version", Run: func([]string) error { return nil }})

	err := app.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("Run() error = nil, want error for unknown command")
	}
	if !strings.Contains(buf.String(), `unknown command "bogus"`) {
		t.Errorf("Run() output = %q, want it to name the unknown command", buf.String())
	}
}

func TestRegisterDuplicateNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register() with duplicate name did not panic")
		}
	}()

	app := cli.NewApp("edgemesh-cli", &bytes.Buffer{})
	app.Register(cli.Command{Name: "version", Run: func([]string) error { return nil }})
	app.Register(cli.Command{Name: "version", Run: func([]string) error { return nil }})
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register() with empty name did not panic")
		}
	}()

	app := cli.NewApp("edgemesh-cli", &bytes.Buffer{})
	app.Register(cli.Command{Run: func([]string) error { return nil }})
}

func TestCommandErrorPropagates(t *testing.T) {
	app := cli.NewApp("edgemesh-cli", &bytes.Buffer{})
	wantErr := errors.New("boom")
	app.Register(cli.Command{Name: "fail", Run: func([]string) error { return wantErr }})

	if err := app.Run([]string{"fail"}); err != wantErr {
		t.Errorf("Run() error = %v, want %v", err, wantErr)
	}
}
