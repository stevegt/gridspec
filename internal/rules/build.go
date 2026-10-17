package rules

import (
	"fmt"
	"strings"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
)

func checkBuildReadiness(ctx Context, idx Index) []diag.Diagnostic {
	if !ctx.Profile.BuildReady {
		return nil
	}
	var ds []diag.Diagnostic
	hasScenario := false
	for _, graph := range ctx.IR.Graphs {
		if graph.Status != "active" {
			continue
		}
		if graph.Kind == "scenario" {
			hasScenario = true
		}
		contract, hasContract := idx.Contracts[graph.ID]
		if !hasContract {
			ds = append(ds, diagnostic("SDV600", diag.SeverityError, graph.Location, "active graph has no effective contract", "add a fenced specdot contract linked by URL", graph.ID, true))
		}
		if graph.Kind == "machine" && (contract.Machine == nil || contract.Machine.Agent == "") {
			ds = append(ds, diagnostic("SDV601", diag.SeverityError, graph.Location, "build-ready machine is not owned by an agent", "set machine.agent", graph.ID, false))
		}
		if graph.URL == "" {
			ds = append(ds, diagnostic("SDV607", diag.SeverityError, graph.Location, "build-ready graph has no Markdown URL", "link the graph-level contract and rationale", graph.ID, true))
		}
	}
	if !hasScenario {
		loc := model.Location{File: ctx.Config.Path}
		if len(ctx.IR.Graphs) > 0 {
			loc = ctx.IR.Graphs[0].Location
		}
		ds = append(ds, diagnostic("SDV603", diag.SeverityError, loc, "build-ready module has no scenario graph", "add executable success, failure, recovery, and restart scenarios", "", true))
	}
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		for _, latitude := range contract.Latitude {
			if latitude.Area == "" || len(latitude.Allowed) == 0 || latitude.Owner == "" {
				ds = append(ds, diagnostic("SDV604", diag.SeverityError, contract.Location, "implementation latitude lacks area, allowed choices, or owner", "constrain every deliberately open implementation choice", id, false))
			}
		}
		if contract.Data != nil && contract.Data.Secret && contract.Metadata["secret_handling"] == "" && !latitudeAllows(contract, "secret-handling") {
			ds = append(ds, diagnostic("SDV605", diag.SeverityError, contract.Location, "secret data has no handling policy", "specify storage, logging, transport, and rotation rules", id, false))
		}
		if strings.EqualFold(contract.Metadata["trust"], "implicit") {
			ds = append(ds, diagnostic("SDV606", diag.SeverityError, contract.Location, fmt.Sprintf("%s relies on implicit trust", id), "name the trust boundary and authentication/authorization evidence", id, false))
		}
	}
	return ds
}
