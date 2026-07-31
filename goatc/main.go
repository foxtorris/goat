// Command goatc compiles Go tool plugins and assembles them into a standalone
// interactive goat agent binary.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/torrischen/goat/goatc/compiler"
	"github.com/torrischen/goat/goatc/config"
)

var version = "dev"

const usage = `goatc assembles goat agents from YAML.

Usage:
  goatc build [-f goatc.yaml] [-o output]
  goatc validate [-f goatc.yaml]
  goatc version
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "goatc: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("a command is required")
	}

	switch args[0] {
	case "build":
		flags := flag.NewFlagSet("build", flag.ContinueOnError)
		flags.SetOutput(stderr)
		configPath := flags.String("f", "goatc.yaml", "configuration file")
		output := flags.String("o", "", "output binary (overrides build.output)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		result, err := compiler.Build(*configPath, *output, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "built %s\n", result.Output)
		return nil
	case "validate":
		flags := flag.NewFlagSet("validate", flag.ContinueOnError)
		flags.SetOutput(stderr)
		configPath := flags.String("f", "goatc.yaml", "configuration file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if _, err := config.Load(*configPath); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is valid\n", *configPath)
		return nil
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "goatc %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
