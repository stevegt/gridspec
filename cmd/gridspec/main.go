package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "propose":
		return runPropose(args[1:], stdout, stderr)
	case "accept":
		return runAccept(args[1:], stdout, stderr)
	case "vet":
		return runVet(args[1:], stdout, stderr)
	case "view":
		return runView(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "disassemble":
		return runDisassemble(args[1:], stdout, stderr)
	case "describe":
		return runDescribe(args[1:], stdout, stderr)
	case "scenarios":
		return runScenarios(args[1:], stdout, stderr)
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "build":
		return runBuild(args[1:], stdout, stderr)
	case "conform":
		return runConform(args[1:], stdout, stderr)
	case "update":
		return runUpdate(args[1:], stdout, stderr)
	case "fetch":
		return runTransfer("fetch", args[1:], stdout, stderr)
	case "push":
		return runTransfer("push", args[1:], stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "gridspec", version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "gridspec: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: gridspec <command> [options]")
	fmt.Fprintln(writer, "commands:")
	fmt.Fprintln(writer, "  init         initialize a signed transactional project")
	fmt.Fprintln(writer, "  propose      publish a transaction proposal")
	fmt.Fprintln(writer, "  accept       select a proposal in a signed Grid commit")
	fmt.Fprintln(writer, "  vet          validate accepted or proposed specs")
	fmt.Fprintln(writer, "  view         render accepted or proposed specs as text or DOT")
	fmt.Fprintln(writer, "  import       migrate legacy DOT and Markdown")
	fmt.Fprintln(writer, "  disassemble  propose specs from existing Go")
	fmt.Fprintln(writer, "  describe     propose human-readable spec descriptions")
	fmt.Fprintln(writer, "  scenarios    propose missing executable command scenarios")
	fmt.Fprintln(writer, "  diff         compare a proposal with accepted semantic state")
	fmt.Fprintln(writer, "  build        compile accepted or proposed specs into new Go")
	fmt.Fprintln(writer, "  conform      test existing Go against accepted or proposed specs")
	fmt.Fprintln(writer, "  update       update owned Go or preview a proposal")
	fmt.Fprintln(writer, "  fetch/push   transfer GridSpec Git carrier refs")
	fmt.Fprintln(writer, "  version      print the version")
}
