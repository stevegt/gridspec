package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/promisegrid/specdot/internal/app"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "vet":
		return runVet(args[1:], stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "specdot", version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "specdot: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func runVet(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("specdot vet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts app.VetOptions
	flags.StringVar(&opts.ConfigPath, "config", "", "module manifest (discovered upward by default)")
	flags.StringVar(&opts.Profile, "profile", "default", "validation profile")
	flags.StringVar(&opts.Format, "format", "text", "diagnostic format: text, json, or sarif")
	flags.BoolVar(&opts.Online, "online", false, "probe allowed remote Markdown URLs")
	flags.StringVar(&opts.EmitIR, "emit-ir", "", "write canonical IR to a file or -")
	flags.Usage = func() { vetUsage(flags.Output()) }
	ordered, paths, err := reorderVetArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "specdot vet:", err)
		return 2
	}
	if err := flags.Parse(ordered); err != nil {
		return 2
	}
	if opts.Format != "text" && opts.Format != "json" && opts.Format != "sarif" {
		fmt.Fprintf(stderr, "specdot vet: invalid --format %q\n", opts.Format)
		return 2
	}
	opts.Paths, opts.Stdout, opts.Stderr = append(paths, flags.Args()...), stdout, stderr
	return app.Vet(opts)
}

// The public grammar permits paths before or after flags. flag.FlagSet does
// not, so this stable pre-pass separates known option tokens from paths.
func reorderVetArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{"--config": true, "-config": true, "--profile": true, "-profile": true, "--format": true, "-format": true, "--emit-ir": true, "-emit-ir": true}
	boolFlags := map[string]bool{"--online": true, "-online": true}
	var ordered, paths []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name := arg
		if at := strings.IndexByte(arg, '='); at >= 0 {
			name = arg[:at]
		}
		if valueFlags[name] {
			ordered = append(ordered, arg)
			if name == arg {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("%s needs a value", arg)
				}
				i++
				ordered = append(ordered, args[i])
			}
			continue
		}
		if boolFlags[name] || arg == "-h" || arg == "--help" {
			ordered = append(ordered, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			ordered = append(ordered, arg)
			continue
		}
		paths = append(paths, arg)
	}
	return ordered, paths, nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: specdot <command> [options]")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  vet      compile and validate a spec module")
	fmt.Fprintln(w, "  version  print the version")
}

func vetUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: specdot vet [paths...] [--config specdot.yaml] [--profile name]")
	fmt.Fprintln(w, "                   [--format text|json|sarif] [--online] [--emit-ir file|-]")
}
