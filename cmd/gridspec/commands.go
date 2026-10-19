package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/promisegrid/gridspec/internal/app"
	"github.com/promisegrid/gridspec/internal/conform"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/goworkflow"
	"github.com/promisegrid/gridspec/internal/migrate"
	"github.com/promisegrid/gridspec/internal/project"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/report"
	"github.com/promisegrid/gridspec/internal/view"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("project", "", "stable project/module name (defaults to go.mod module)")
	profile := flags.String("profile", "default", "initial validation profile")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if *name == "" {
		*name = inferProjectName(".")
	}
	if *name == "" {
		fmt.Fprintln(stderr, "gridspec init: --project is required when go.mod has no module")
		return 2
	}
	_, snapshot, err := project.Init(context.Background(), project.InitOptions{Dir: ".", Name: *name, Profile: *profile})
	if err != nil {
		fmt.Fprintln(stderr, "gridspec init:", err)
		return 2
	}
	fmt.Fprintf(stdout, "initialized %s\ncommit %s\nstate %s\n", *name, snapshot.Commit.CID, snapshot.State.CID)
	return 0
}

func runPropose(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec propose", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authored := flags.Bool("authored", false, "sign independent human authorship (acceptance is always signed separately)")
	if err := flags.Parse(args); err != nil || len(flags.Args()) > 1 {
		return 2
	}
	path := "-"
	if len(flags.Args()) == 1 {
		path = flags.Args()[0]
	}
	program, err := readInput(path)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec propose:", err)
		return 2
	}
	current, err := project.Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(stderr, "gridspec propose:", err)
		return 2
	}
	proposal, err := current.Propose(context.Background(), program, *authored)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec propose:", err)
		return 1
	}
	fmt.Fprintf(stdout, "proposal %s\nref %s\nstate %s\n", proposal.Event.CID, proposal.Ref, proposal.State.CID)
	return 0
}

func runAccept(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec accept", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 1 {
		fmt.Fprintln(stderr, "usage: gridspec accept <proposal-CID-or-ref>")
		return 2
	}
	current, err := project.Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(stderr, "gridspec accept:", err)
		return 2
	}
	snapshot, err := current.Accept(context.Background(), flags.Args()[0])
	if err != nil {
		fmt.Fprintln(stderr, "gridspec accept:", err)
		return 1
	}
	fmt.Fprintf(stdout, "accepted %s\ncommit %s\nstate %s\n", snapshot.Head.CID, snapshot.Commit.CID, snapshot.State.CID)
	return 0
}

func runVet(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec vet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("profile", "", "validation profile (accepted profile by default)")
	format := flags.String("format", "text", "text, json, or sarif")
	online := flags.Bool("online", false, "probe allowed remote Markdown URLs")
	emitIR := flags.String("emit-ir", "", "write derived compatibility IR to a file or -")
	proposal := flags.String("proposal", "", "use a current proposal event CID or ref")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if *format != "text" && *format != "json" && *format != "sarif" {
		fmt.Fprintf(stderr, "gridspec vet: invalid --format %q\n", *format)
		return 2
	}
	_, source, ok := loadProjectSource(stderr, "vet", *proposal)
	if !ok {
		return 2
	}
	return app.VetState(app.StateVetOptions{State: source.State, Profile: *profile, Format: *format, Online: *online, EmitIR: *emitIR, Stdout: stdout, Stderr: stderr})
}

func runView(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec view", flag.ContinueOnError)
	flags.SetOutput(stderr)
	formatName := flags.String("format", "text", "text or dot")
	graph := flags.String("graph", "", "for DOT, render one graph instead of the compact overview")
	output := flags.String("output", "-", "output file or -")
	proposal := flags.String("proposal", "", "use a current proposal event CID or ref")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	_, source, ok := loadProjectSource(stderr, "view", *proposal)
	if !ok {
		return 2
	}
	var data []byte
	var err error
	switch *formatName {
	case "text":
		data, err = view.Text(source.State)
	case "dot":
		data, err = view.DOT(source.State, *graph)
	default:
		err = fmt.Errorf("invalid format %q", *formatName)
	}
	if err == nil {
		err = writeOutput(*output, data, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "gridspec view:", err)
		return 2
	}
	return 0
}

func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "legacy" {
		fmt.Fprintln(stderr, "usage: gridspec import legacy [--config specdot.yaml] [--profile name]")
		return 2
	}
	flags := flag.NewFlagSet("gridspec import legacy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("config", "specdot.yaml", "legacy Specdot manifest")
	profile := flags.String("profile", "build", "legacy validation profile")
	online := flags.Bool("online", false, "probe allowed remote URLs during migration")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	legacy, err := app.LoadLegacyModule(*manifest, *profile, *online)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec import legacy:", err)
		return 2
	}
	if diag.HasErrors(legacy.Diagnostics) {
		_ = report.Write(stdout, "text", legacy.IR.Module, legacy.IR.Digest, legacy.Diagnostics)
		return 1
	}
	current, err := project.Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintln(stderr, "gridspec import legacy:", err)
		return 2
	}
	if current.Name != legacy.IR.Module {
		fmt.Fprintf(stderr, "gridspec import legacy: initialized project %q does not match legacy module %q\n", current.Name, legacy.IR.Module)
		return 2
	}
	snapshot, err := current.Load(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "gridspec import legacy:", err)
		return 2
	}
	program, err := migrate.LegacyProgram(legacy.IR.Module, legacy.ProfileName, legacy.IR, legacy.Documents, snapshot.State)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec import legacy:", err)
		return 2
	}
	proposal, err := current.Propose(context.Background(), program, false)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec import legacy:", err)
		return 1
	}
	fmt.Fprintf(stdout, "proposal %s\nref %s\nlegacy_digest %s\n", proposal.Event.CID, proposal.Ref, legacy.IR.Digest)
	return 0
}

func runDisassemble(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec disassemble", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", ".", "existing Go module root")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	current, selected, ok := loadProjectSource(stderr, "disassemble", "")
	if !ok {
		return 2
	}
	model, err := provider.FromEnvironment()
	if err != nil {
		fmt.Fprintln(stderr, "gridspec disassemble:", err)
		return 2
	}
	program, response, err := goworkflow.Disassemble(context.Background(), model, current.Repository, selected.Identity, *source)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec disassemble:", err)
		return 1
	}
	proposal, err := current.Propose(context.Background(), program, false)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec disassemble:", err)
		return 1
	}
	fmt.Fprintf(stdout, "proposal %s\nprovider %s\nresponse %s\n", proposal.Event.CID, response.Model, response.ID)
	return 0
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new output directory (required)")
	proposal := flags.String("proposal", "", "build a current proposal event CID or ref")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || *output == "" {
		fmt.Fprintln(stderr, "usage: gridspec build --output <new-directory> [--proposal <CID-or-ref>]")
		return 2
	}
	current, selected, ok := loadProjectSource(stderr, "build", *proposal)
	if !ok {
		return 2
	}
	model, err := provider.FromEnvironment()
	if err != nil {
		fmt.Fprintln(stderr, "gridspec build:", err)
		return 2
	}
	generated, err := goworkflow.Build(context.Background(), model, current.Repository, selected.Identity, *output)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec build:", err)
		return 1
	}
	digest, err := goworkflow.Digest(generated)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec build:", err)
		return 2
	}
	generator := generated.ProviderModel + "/" + generated.ResponseID
	target := "go-build"
	if selected.Identity.IsProposal() {
		target = "go-build-preview"
	}
	candidate, err := current.PublishCandidate(context.Background(), selected.Identity, target, generator, digest, []byte("go test ./... passed\n"))
	if err != nil {
		fmt.Fprintln(stderr, "gridspec build: publish candidate:", err)
		return 1
	}
	fmt.Fprintf(stdout, "built %d file(s) in %s\ncandidate %s\n", len(generated.Files), *output, candidate.Artifact.CID)
	return 0
}

func runConform(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec conform", flag.ContinueOnError)
	flags.SetOutput(stderr)
	implementation := flags.String("implementation", conform.DefaultFilename, "implementation sidecar")
	profile := flags.String("profile", "", "validation profile")
	formatName := flags.String("format", "text", "text or json")
	emitBaseline := flags.String("emit-baseline", "", "write a reviewed baseline candidate")
	record := flags.Bool("record", false, "publish exact JSON conformance evidence as an unsigned candidate")
	proposal := flags.String("proposal", "", "compare against a current proposal event CID or ref")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if *formatName != "text" && *formatName != "json" {
		fmt.Fprintf(stderr, "gridspec conform: invalid --format %q\n", *formatName)
		return 2
	}
	if *record && *formatName != "json" {
		fmt.Fprintln(stderr, "gridspec conform: --record requires --format json")
		return 2
	}
	if *proposal != "" && (*record || *emitBaseline != "") {
		fmt.Fprintln(stderr, "gridspec conform: proposal conformance cannot record evidence or emit a baseline")
		return 2
	}
	current, selected, ok := loadProjectSource(stderr, "conform", *proposal)
	if !ok {
		return 2
	}
	reportWriter := stdout
	var recorded bytes.Buffer
	if *record {
		reportWriter = &recorded
	}
	code := app.ConformState(app.StateConformOptions{State: selected.State, ImplementationPath: *implementation, Profile: *profile, Format: *formatName, EmitBaseline: *emitBaseline, Stdout: reportWriter, Stderr: stderr})
	if !*record || code == 2 {
		return code
	}
	if _, err := stdout.Write(recorded.Bytes()); err != nil {
		fmt.Fprintln(stderr, "gridspec conform:", err)
		return 2
	}
	var reportParts struct {
		Evidence    json.RawMessage `json:"evidence"`
		Exclusions  json.RawMessage `json:"exclusions"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(recorded.Bytes(), &reportParts); err != nil {
		fmt.Fprintln(stderr, "gridspec conform: record report:", err)
		return 2
	}
	if len(reportParts.Exclusions) == 0 {
		reportParts.Exclusions = json.RawMessage("[]")
	}
	if len(reportParts.Diagnostics) == 0 {
		reportParts.Diagnostics = json.RawMessage("[]")
	}
	configuration, err := os.ReadFile(*implementation)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec conform: record configuration:", err)
		return 2
	}
	implementationConfig, err := conform.Load(*implementation)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec conform: record configuration:", err)
		return 2
	}
	bundle, err := goworkflow.Scan(implementationConfig.ModuleRoot)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec conform: record implementation:", err)
		return 2
	}
	digest, err := goworkflow.BundleDigest(bundle)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec conform: record implementation:", err)
		return 2
	}
	candidate, err := current.PublishConformanceEvidence(context.Background(), selected.Identity, digest, configuration, reportParts.Evidence, reportParts.Exclusions, reportParts.Diagnostics, recorded.Bytes())
	if err != nil {
		fmt.Fprintln(stderr, "gridspec conform: publish evidence:", err)
		return 2
	}
	fmt.Fprintln(stderr, "gridspec conform: evidence", candidate.Evidence.CID)
	return code
}

func runUpdate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gridspec update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	goRoot := flags.String("source", ".", "existing Go module root")
	write := flags.Bool("write", false, "write owned changes after validation and tests")
	proposal := flags.String("proposal", "", "preview a current proposal event CID or ref")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if *proposal != "" && *write {
		fmt.Fprintln(stderr, "gridspec update: --write requires accepted specifications")
		return 2
	}
	current, selected, ok := loadProjectSource(stderr, "update", *proposal)
	if !ok {
		return 2
	}
	model, err := provider.FromEnvironment()
	if err != nil {
		fmt.Fprintln(stderr, "gridspec update:", err)
		return 2
	}
	result, err := goworkflow.Update(context.Background(), model, current.Repository, selected.Identity, *goRoot, *write)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec update:", err)
		return 1
	}
	for _, change := range result.Changes {
		fmt.Fprintln(stdout, change)
	}
	if len(result.Changes) == 0 {
		fmt.Fprintln(stdout, "no changes")
	} else if !*write {
		fmt.Fprintln(stdout, "dry run; rerun with --write after review")
	}
	digest, err := goworkflow.Digest(result.Generated)
	if err != nil {
		fmt.Fprintln(stderr, "gridspec update:", err)
		return 2
	}
	evidence := "dry run: syntax, formatting, path, and ownership checks passed; Go tests not run\n"
	if *write {
		evidence = "owned files updated and go test ./... passed\n"
	}
	generator := result.Generated.ProviderModel + "/" + result.Generated.ResponseID
	target := "go-update"
	if selected.Identity.IsProposal() {
		target = "go-update-preview"
	}
	candidate, err := current.PublishCandidate(context.Background(), selected.Identity, target, generator, digest, []byte(evidence))
	if err != nil {
		fmt.Fprintln(stderr, "gridspec update: publish candidate:", err)
		return 1
	}
	fmt.Fprintln(stdout, "candidate", candidate.Artifact.CID)
	return 0
}

func runTransfer(operation string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintf(stderr, "usage: gridspec %s [remote]\n", operation)
		return 2
	}
	remote := "origin"
	if len(args) == 1 {
		remote = args[0]
	}
	current, err := project.Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintf(stderr, "gridspec %s: %v\n", operation, err)
		return 2
	}
	if operation == "fetch" {
		err = current.Repository.Fetch(context.Background(), remote)
	} else {
		err = current.Repository.Push(context.Background(), remote)
	}
	if err != nil {
		fmt.Fprintf(stderr, "gridspec %s: %v\n", operation, err)
		return 1
	}
	fmt.Fprintf(stdout, "%sed GridSpec refs with %s\n", operation, remote)
	return 0
}

func loadProjectSource(stderr io.Writer, operation, proposal string) (*project.Project, project.Source, bool) {
	current, err := project.Open(context.Background(), ".")
	if err != nil {
		fmt.Fprintf(stderr, "gridspec %s: %v\n", operation, err)
		return nil, project.Source{}, false
	}
	selected, err := current.LoadSource(context.Background(), proposal)
	if err != nil {
		fmt.Fprintf(stderr, "gridspec %s: %v\n", operation, err)
		return nil, project.Source{}, false
	}
	return current, selected, true
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
	}
	return os.ReadFile(path)
}

func writeOutput(path string, data []byte, stdout io.Writer) error {
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(abs), ".gridspec-view-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, abs)
}

func inferProjectName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err == nil {
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			fields := strings.Fields(string(line))
			if len(fields) == 2 && fields[0] == "module" {
				return fields[1]
			}
		}
	}
	abs, _ := filepath.Abs(dir)
	return filepath.Base(abs)
}
