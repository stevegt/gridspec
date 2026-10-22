// Package scenarios validates accepted command-scenario contracts and turns
// missing behavioral oracles into bounded, audited proposal transactions.
package scenarios

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

const (
	MaxSteps        = 64
	MaxRepeat       = 10
	MaxFixtureBytes = 8 << 20
	MaxStreamBytes  = 1 << 20
	DefaultTimeout  = 5 * time.Second
	MaxTimeout      = 60 * time.Second
)

var artifactName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type Definition struct {
	ID       string
	Location model.Location
	Contract model.ScenarioContract
}

type Problem struct {
	ScenarioID string
	Location   model.Location
	Message    string
}

// MissingError means compilation has no complete accepted command oracle.
// Callers may use Missing to create a proposal, but must not emit code.
type MissingError struct {
	IDs []string
}

func (e *MissingError) Error() string {
	if len(e.IDs) == 0 {
		return "accepted specifications contain no active command scenarios"
	}
	return "accepted command scenarios are missing executable contracts: " + strings.Join(e.IDs, ", ")
}

// Definitions returns the complete, sorted accepted command oracle.
func Definitions(state specstate.State) ([]Definition, error) {
	ir, _, _, err := state.SemanticView()
	if err != nil {
		return nil, err
	}
	problems := Validate(ir)
	if len(problems) != 0 {
		messages := make([]string, len(problems))
		for i, problem := range problems {
			messages[i] = problem.ScenarioID + ": " + problem.Message
		}
		return nil, fmt.Errorf("invalid accepted command scenarios: %s", strings.Join(messages, "; "))
	}
	contracts := make(map[string]model.Contract)
	for _, contract := range ir.Contracts {
		contracts[contract.For] = contract
	}
	var result []Definition
	for _, graph := range ir.Graphs {
		if graph.Status != "active" || graph.Kind != "scenario" {
			continue
		}
		contract := contracts[graph.ID]
		if !activeContract(contract) || contract.Scenario == nil {
			continue
		}
		result = append(result, Definition{ID: graph.ID, Location: contract.Location, Contract: *contract.Scenario})
	}
	if len(result) == 0 {
		return nil, &MissingError{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// Missing identifies whether compilation lacks an executable command oracle.
// An explicit incomplete scenario is returned for repair. Automatic discovery
// returns one empty marker when no complete oracle exists, allowing descriptive
// scenario graphs to remain descriptive while a compiler-owned oracle is added.
func Missing(state specstate.State, graphID string) ([]string, error) {
	ir, _, _, err := state.SemanticView()
	if err != nil {
		return nil, err
	}
	contracts := make(map[string]model.Contract)
	for _, contract := range ir.Contracts {
		contracts[contract.For] = contract
	}
	if graphID != "" {
		for _, graph := range ir.Graphs {
			if graph.ID != graphID {
				continue
			}
			if graph.Status != "active" {
				return nil, fmt.Errorf("graph %q is not active", graphID)
			}
			if graph.Kind != "scenario" {
				return []string{""}, nil
			}
			contract := contracts[graph.ID]
			if !activeContract(contract) || contract.Scenario == nil {
				return []string{graph.ID}, nil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("graph %q does not exist", graphID)
	}
	complete := 0
	for _, graph := range ir.Graphs {
		if graph.Status == "active" && graph.Kind == "scenario" {
			contract := contracts[graph.ID]
			if activeContract(contract) && contract.Scenario != nil {
				complete++
			}
		}
	}
	if complete == 0 {
		return []string{""}, nil
	}
	return nil, nil
}

// Validate reports every malformed or incomplete accepted scenario contract.
func Validate(ir model.IR) []Problem {
	known := make(map[string]bool)
	scenarioGraphs := make(map[string]model.Graph)
	activeScenarioGraphs := make(map[string]model.Graph)
	for _, graph := range ir.Graphs {
		if graph.Status == "active" {
			known[graph.ID] = true
		}
		if graph.Kind == "scenario" {
			scenarioGraphs[graph.ID] = graph
			if graph.Status == "active" {
				activeScenarioGraphs[graph.ID] = graph
			}
		}
		for _, element := range append(append([]model.Element(nil), graph.Nodes...), graph.Edges...) {
			if element.Status == "active" {
				known[element.ID] = true
			}
		}
	}
	contracts := make(map[string]model.Contract)
	for _, contract := range ir.Contracts {
		contracts[contract.For] = contract
		if contract.Status == "" || contract.Status == "active" {
			for _, claim := range contract.Claims {
				known[claim.ID] = true
			}
		}
	}
	var result []Problem
	for id := range activeScenarioGraphs {
		contract, ok := contracts[id]
		if !ok || !activeContract(contract) || contract.Scenario == nil {
			continue
		}
		if contract.Kind != "scenario" {
			result = append(result, Problem{ScenarioID: id, Location: contract.Location, Message: "executable scenario contract kind must be scenario"})
		}
		result = append(result, validateContract(id, contract.Location, *contract.Scenario, known)...)
	}
	for _, contract := range ir.Contracts {
		if activeContract(contract) && contract.Scenario != nil && scenarioGraphs[contract.For].ID == "" {
			result = append(result, Problem{ScenarioID: contract.For, Location: contract.Location, Message: "scenario contract must extend a scenario graph"})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ScenarioID != result[j].ScenarioID {
			return result[i].ScenarioID < result[j].ScenarioID
		}
		return result[i].Message < result[j].Message
	})
	return result
}

func activeContract(contract model.Contract) bool {
	return contract.For != "" && (contract.Status == "" || contract.Status == "active")
}

func validateContract(id string, location model.Location, contract model.ScenarioContract, known map[string]bool) []Problem {
	problem := func(format string, args ...any) Problem {
		return Problem{ScenarioID: id, Location: location, Message: fmt.Sprintf(format, args...)}
	}
	var result []Problem
	if !artifactName.MatchString(contract.Artifact) {
		result = append(result, problem("artifact must contain only letters, digits, hyphens, and underscores"))
	}
	if contract.Repeat < 1 || contract.Repeat > MaxRepeat {
		result = append(result, problem("repeat must be between 1 and %d", MaxRepeat))
	}
	if len(contract.Covers) == 0 {
		result = append(result, problem("covers must name at least one semantic ID"))
	}
	seenCovers := make(map[string]bool)
	for _, covered := range contract.Covers {
		if !txnlang.IsIdentifier(covered) || !known[covered] {
			result = append(result, problem("covers references unknown semantic ID %q", covered))
		} else if seenCovers[covered] {
			result = append(result, problem("covers duplicates semantic ID %q", covered))
		}
		seenCovers[covered] = true
	}
	fixtureBytes := 0
	fixturePaths := make(map[string]bool)
	for _, fixture := range contract.Fixtures {
		if err := SafePath(fixture.Path); err != nil {
			result = append(result, problem("fixture path %q: %v", fixture.Path, err))
		} else if fixturePaths[fixture.Path] {
			result = append(result, problem("fixture path %q is duplicated", fixture.Path))
		}
		fixturePaths[fixture.Path] = true
		fixtureBytes += len(fixture.Content)
	}
	for left := range fixturePaths {
		for right := range fixturePaths {
			if left != right && strings.HasPrefix(right, left+"/") {
				result = append(result, problem("fixture paths %q and %q conflict as file and descendant", left, right))
			}
		}
	}
	if fixtureBytes > MaxFixtureBytes {
		result = append(result, problem("fixture content exceeds %d bytes", MaxFixtureBytes))
	}
	if len(contract.Steps) == 0 || len(contract.Steps) > MaxSteps {
		result = append(result, problem("steps must contain between 1 and %d entries", MaxSteps))
	}
	stepIDs := make(map[string]bool)
	for index, step := range contract.Steps {
		label := fmt.Sprintf("step %d", index+1)
		if !txnlang.IsIdentifier(step.ID) {
			result = append(result, problem("%s has invalid ID %q", label, step.ID))
		} else if stepIDs[step.ID] {
			result = append(result, problem("step ID %q is duplicated", step.ID))
		}
		stepIDs[step.ID] = true
		if len(step.Stdin) > MaxStreamBytes {
			result = append(result, problem("%s stdin exceeds %d bytes", label, MaxStreamBytes))
		}
		for _, arg := range step.Args {
			if strings.IndexByte(arg, 0) >= 0 {
				result = append(result, problem("%s argument contains NUL", label))
			}
		}
		if _, err := Timeout(step.Timeout); err != nil {
			result = append(result, problem("%s timeout: %v", label, err))
		}
		if step.Expect.ExitCode == nil || step.Expect.Stdout == nil || step.Expect.Stderr == nil {
			result = append(result, problem("%s must declare exact exit_code, stdout, and stderr expectations", label))
		} else {
			if *step.Expect.ExitCode < 0 || *step.Expect.ExitCode > 255 {
				result = append(result, problem("%s exit_code must be between 0 and 255", label))
			}
			if len(*step.Expect.Stdout) > MaxStreamBytes || len(*step.Expect.Stderr) > MaxStreamBytes {
				result = append(result, problem("%s stream expectation exceeds %d bytes", label, MaxStreamBytes))
			}
		}
		filePaths := make(map[string]bool)
		for _, file := range step.Expect.Files {
			if err := SafePath(file.Path); err != nil {
				result = append(result, problem("%s expected file path %q: %v", label, file.Path, err))
			} else if filePaths[file.Path] {
				result = append(result, problem("%s expected file path %q is duplicated", label, file.Path))
			}
			filePaths[file.Path] = true
			if file.Exists == nil {
				result = append(result, problem("%s expected file %q must declare exists", label, file.Path))
				continue
			}
			contentChoices := 0
			if file.Text != nil {
				contentChoices++
				if len(*file.Text) > MaxStreamBytes {
					result = append(result, problem("%s expected file %q text exceeds %d bytes", label, file.Path, MaxStreamBytes))
				}
			}
			if file.JSON != nil {
				contentChoices++
				if len(*file.JSON) > MaxStreamBytes || !json.Valid([]byte(*file.JSON)) {
					result = append(result, problem("%s expected file %q has invalid or oversized JSON", label, file.Path))
				}
			}
			if *file.Exists && contentChoices != 1 {
				result = append(result, problem("%s expected existing file %q must declare exactly one of text or json", label, file.Path))
			}
			if !*file.Exists && contentChoices != 0 {
				result = append(result, problem("%s expected absent file %q cannot declare content", label, file.Path))
			}
		}
	}
	return result
}

func SafePath(value string) error {
	if value == "" || strings.ContainsAny(value, "\\:") || strings.IndexByte(value, 0) >= 0 || path.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("must be a nonempty relative slash path")
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("must be canonical and remain inside the workspace")
	}
	return nil
}

func Timeout(value string) (time.Duration, error) {
	if value == "" {
		return DefaultTimeout, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 || duration > MaxTimeout {
		return 0, fmt.Errorf("must be positive and at most %s", MaxTimeout)
	}
	return duration, nil
}
