// Package rules implements GridSpec's semantic vet passes.
package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/source"
)

type Context struct {
	IR           *model.IR
	Config       config.Config
	Profile      config.Profile
	Documents    map[string]source.Document
	RawContracts []model.Contract
	Online       bool
	Now          time.Time
}

type Index struct {
	Elements       map[string]model.Element
	Graphs         map[string]model.Graph
	Contracts      map[string]model.Contract
	ElementGraph   map[string]model.Graph
	AllLocations   map[string]model.Location
	CaseFolded     map[string]string
	ContractSource map[string]model.Location
}

// Run executes all semantic passes, derives promise bindings, applies profile
// severity, and finally applies valid waivers.
func Run(ctx Context) ([]diag.Diagnostic, []model.Binding) {
	if ctx.Now.IsZero() {
		ctx.Now = time.Now()
	}
	idx, ds := buildIndex(ctx)
	if len(ctx.IR.Graphs) == 0 {
		ds = append(ds, diagnostic("SDV000", diag.SeverityError, model.Location{File: ctx.Config.Path}, "module contains no semantic graphs", "add at least one graph entity in a transaction", "", false))
	}
	ds = append(ds, checkLinks(ctx, idx)...)
	ds = append(ds, checkGeneric(ctx, idx)...)
	ds = append(ds, checkAgentsAndSignals(ctx, idx)...)
	promiseDS, bindings := checkPromises(ctx, idx)
	ds = append(ds, promiseDS...)
	ds = append(ds, checkFlowGraphs(ctx, idx)...)
	ds = append(ds, checkMachines(ctx, idx)...)
	ds = append(ds, checkData(ctx, idx)...)
	ds = append(ds, checkEffectsAndChannels(ctx, idx)...)
	ds = append(ds, checkBuildReadiness(ctx, idx)...)
	ds = applyProfile(ctx.Profile, ds)
	ds = applyWaivers(ctx, idx, ds)
	diag.Sort(ds)
	return ds, bindings
}

var validID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:/-]*$`)

func buildIndex(ctx Context) (Index, []diag.Diagnostic) {
	idx := Index{
		Elements: make(map[string]model.Element), Graphs: make(map[string]model.Graph),
		Contracts: make(map[string]model.Contract), ElementGraph: make(map[string]model.Graph),
		AllLocations: make(map[string]model.Location), CaseFolded: make(map[string]string),
		ContractSource: make(map[string]model.Location),
	}
	var ds []diag.Diagnostic
	register := func(id string, loc model.Location, kind string) {
		if id == "" {
			return
		}
		if !validID.MatchString(id) {
			ds = append(ds, diagnostic("SDV100", diag.SeverityError, loc, fmt.Sprintf("%s id %q is not a valid semantic identifier", kind, id), "use letters, digits, dots, colons, slashes, underscores, or hyphens; start with a letter", id, false))
		}
		if first, exists := idx.AllLocations[id]; exists {
			d := diagnostic("SDV101", diag.SeverityError, loc, fmt.Sprintf("semantic id %q is declared more than once", id), "every graph, node, edge, contract target, and claim id is module-wide unique", id, false)
			d.Related = []diag.RelatedLocation{{Location: toDiag(first), Message: "first declaration"}}
			ds = append(ds, d)
		} else {
			idx.AllLocations[id] = loc
		}
		fold := strings.ToLower(id)
		if other, exists := idx.CaseFolded[fold]; exists && other != id {
			ds = append(ds, diagnostic("SDV102", diag.SeverityError, loc, fmt.Sprintf("id %q differs from %q only by case", id, other), "semantic ids must remain unambiguous on case-insensitive filesystems", id, false))
		} else {
			idx.CaseFolded[fold] = id
		}
	}
	for _, graph := range ctx.IR.Graphs {
		register(graph.ID, graph.Location, "graph")
		if _, exists := idx.Graphs[graph.ID]; !exists {
			idx.Graphs[graph.ID] = graph
		}
		for _, element := range append(append([]model.Element(nil), graph.Nodes...), graph.Edges...) {
			for key := range element.Attrs {
				if strings.HasPrefix(key, "specdot_") && !knownSpecdotAttr[key] {
					ds = append(ds, diagnostic("SDV120", diag.SeverityError, element.Location, fmt.Sprintf("unknown semantic attribute %q", key), "correct the spelling or extend the versioned language schema", element.ID, false))
				}
			}
			register(element.ID, element.Location, element.Element)
			if _, exists := idx.Elements[element.ID]; !exists {
				idx.Elements[element.ID] = element
				idx.ElementGraph[element.ID] = graph
			}
		}
	}
	// Contracts extend declarations and therefore do not reserve the target id.
	// More than one extension is nevertheless ambiguous.
	for _, contract := range ctx.RawContracts {
		if first, exists := idx.ContractSource[contract.For]; exists {
			d := diagnostic("SDV103", diag.SeverityError, contract.Location, fmt.Sprintf("more than one Markdown contract extends %q", contract.For), "merge the contract into one fenced specdot block", contract.For, false)
			d.Related = []diag.RelatedLocation{{Location: toDiag(first), Message: "first extension"}}
			ds = append(ds, d)
		} else {
			idx.ContractSource[contract.For] = contract.Location
		}
	}
	for _, contract := range ctx.IR.Contracts {
		if _, exists := idx.Contracts[contract.For]; !exists {
			idx.Contracts[contract.For] = contract
		}
		if _, element := idx.Elements[contract.For]; !element {
			if _, graph := idx.Graphs[contract.For]; !graph {
				ds = append(ds, diagnostic("SDV104", diag.SeverityError, contract.Location, fmt.Sprintf("contract target %q does not exist", contract.For), "declare the semantic id in a DOT graph", contract.For, false))
			}
		}
		if contract.Kind != "" {
			targetKind := ""
			if element, ok := idx.Elements[contract.For]; ok {
				targetKind = element.Kind
			}
			if graph, ok := idx.Graphs[contract.For]; ok {
				targetKind = graph.Kind
			}
			if targetKind != "" && contract.Kind != targetKind {
				ds = append(ds, diagnostic("SDV107", diag.SeverityError, contract.Location, fmt.Sprintf("contract kind %q does not match target kind %q", contract.Kind, targetKind), "make the Markdown extension agree with its DOT declaration", contract.For, false))
			}
		}
		claimIDs := make(map[string]bool)
		for _, claim := range contract.Claims {
			if claim.ID == "" {
				ds = append(ds, diagnostic("SDV105", diag.SeverityError, contract.Location, "claim is missing id", "give every claim a stable id", contract.For, false))
			} else if claimIDs[claim.ID] {
				ds = append(ds, diagnostic("SDV106", diag.SeverityError, contract.Location, fmt.Sprintf("claim id %q is duplicated within the contract", claim.ID), "claim ids must be unique", contract.For, false))
			}
			claimIDs[claim.ID] = true
			if claim.ID != "" {
				register(claim.ID, contract.Location, "claim")
			}
		}
	}
	return idx, ds
}

func checkGeneric(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	validStatus := map[string]bool{"active": true, "draft": true, "historical": true, "superseded": true}
	for _, graph := range ctx.IR.Graphs {
		for key := range graph.Attrs {
			if strings.HasPrefix(key, "specdot_") && !knownSpecdotAttr[key] {
				ds = append(ds, diagnostic("SDV120", diag.SeverityError, graph.Location, fmt.Sprintf("unknown semantic attribute %q", key), "correct the spelling or extend the versioned language schema", graph.ID, false))
			}
		}
		if !validStatus[graph.Status] {
			ds = append(ds, diagnostic("SDV110", diag.SeverityError, graph.Location, fmt.Sprintf("invalid status %q", graph.Status), "use active, draft, historical, or superseded", graph.ID, true))
		}
		for _, dependency := range split(graph.Attrs["specdot_depends_on"]) {
			if target, ok := idx.Graphs[dependency]; ok {
				if graph.Status == "active" && (target.Status == "superseded" || target.Status == "historical") {
					ds = append(ds, diagnostic("SDV111", diag.SeverityError, graph.Location, fmt.Sprintf("active graph depends on %s graph %q", target.Status, dependency), "point at an active graph", graph.ID, true))
				}
			} else if _, ok := idx.Elements[dependency]; !ok {
				ds = append(ds, diagnostic("SDV112", diag.SeverityError, graph.Location, fmt.Sprintf("graph dependency %q does not exist", dependency), "declare the target or correct specdot_depends_on", graph.ID, false))
			}
		}
		for _, element := range append(append([]model.Element(nil), graph.Nodes...), graph.Edges...) {
			if !validStatus[element.Status] {
				ds = append(ds, diagnostic("SDV110", diag.SeverityError, element.Location, fmt.Sprintf("invalid status %q", element.Status), "use active, draft, historical, or superseded", element.ID, true))
			}
			if element.Element == "edge" && element.Status == "active" {
				for _, endpoint := range []string{element.From, element.To} {
					if target, ok := idx.Elements[endpoint]; ok && (target.Status == "superseded" || target.Status == "historical") {
						ds = append(ds, diagnostic("SDV111", diag.SeverityError, element.Location, fmt.Sprintf("active edge depends on %s node %q", target.Status, endpoint), "retire the edge or point it at an active declaration", element.ID, true))
					}
				}
			}
			for _, dependency := range split(element.Attrs["specdot_depends_on"]) {
				target, ok := idx.Elements[dependency]
				if !ok {
					if _, ok := idx.Graphs[dependency]; !ok {
						ds = append(ds, diagnostic("SDV112", diag.SeverityError, element.Location, fmt.Sprintf("dependency %q does not exist", dependency), "declare the target or correct specdot_depends_on", element.ID, false))
					}
				} else if element.Status == "active" && (target.Status == "superseded" || target.Status == "historical") {
					ds = append(ds, diagnostic("SDV111", diag.SeverityError, element.Location, fmt.Sprintf("active element depends on %s element %q", target.Status, dependency), "point at an active declaration", element.ID, true))
				}
			}
			role := element.Attrs["specdot_role"]
			if role == "input" || role == "output" {
				if element.Attrs["specdot_type"] == "" {
					ds = append(ds, diagnostic("SDV113", diag.SeverityError, element.Location, fmt.Sprintf("%s node has no specdot_type", role), "type every module boundary", element.ID, false))
				}
				accounts := split(element.Attrs["specdot_accounted_by"])
				if len(accounts) == 0 {
					ds = append(ds, diagnostic("SDV114", diag.SeverityError, element.Location, fmt.Sprintf("%s node is not accounted for by another spec", role), "set specdot_accounted_by to one or more semantic ids in another DOT file", element.ID, false))
				}
				for _, account := range accounts {
					target, ok := idx.Elements[account]
					if !ok {
						ds = append(ds, diagnostic("SDV115", diag.SeverityError, element.Location, fmt.Sprintf("accounting target %q does not exist", account), "reference a promise, channel, or data declaration", element.ID, false))
						continue
					}
					targetGraph := idx.ElementGraph[target.ID]
					if targetGraph.Location.File == graph.Location.File {
						ds = append(ds, diagnostic("SDV116", diag.SeverityError, element.Location, fmt.Sprintf("boundary is accounted for by %q in the same DOT file", account), "account module boundaries in another DOT file", element.ID, false))
					}
					if target.Kind != "promise" && target.Kind != "channel" && target.Kind != "type" && target.Kind != "resource" && target.Kind != "data" {
						ds = append(ds, diagnostic("SDV117", diag.SeverityError, element.Location, fmt.Sprintf("%q is a %s, not an interface-bearing declaration", account, target.Kind), "reference a promise, channel, or data declaration", element.ID, false))
					}
					if left, right := element.Attrs["specdot_type"], target.Attrs["specdot_type"]; left != "" && right != "" && left != right {
						ds = append(ds, diagnostic("SDV118", diag.SeverityError, element.Location, fmt.Sprintf("boundary type %q does not match accounting type %q", left, right), "use the same nominal type", element.ID, false))
					}
				}
			}
			if shape := element.Attrs["shape"]; shape == "diamond" && element.Kind != "decision" {
				ds = append(ds, diagnostic("SDV119", diag.SeverityWarning, element.Location, "diamond shape implies a decision visually, but specdot_kind is not decision", "encode semantics explicitly; rendering attributes are never authoritative", element.ID, true))
			}
		}
	}
	return ds
}

var knownSpecdotAttr = map[string]bool{
	"specdot_version": true, "specdot_id": true, "specdot_kind": true, "specdot_status": true,
	"specdot_agent": true, "specdot_semantics": true, "specdot_accepts": true, "specdot_depends_on": true,
	"specdot_role": true, "specdot_type": true, "specdot_accounted_by": true, "specdot_initial": true,
	"specdot_final": true, "specdot_restart": true, "specdot_branch": true, "specdot_parent": true,
	"specdot_region": true, "specdot_defer": true, "specdot_refuse": true, "specdot_ignore": true,
	"specdot_nonterminating": true, "specdot_entry": true, "specdot_exit": true,
	"specdot_promiser": true, "specdot_promisee": true, "specdot_polarity": true, "specdot_external": true,
	"specdot_prerequisite": true, "specdot_condition": true, "specdot_evidence": true, "specdot_scope": true,
	"specdot_constraints": true, "specdot_resource": true, "specdot_observer": true, "specdot_subject": true,
	"specdot_predicate": true, "specdot_controls": true, "specdot_capabilities": true, "specdot_machines": true,
	"specdot_members": true, "specdot_event": true, "specdot_after": true, "specdot_guard": true,
	"specdot_action": true, "specdot_raise": true, "specdot_priority": true, "specdot_nondeterministic": true,
	"specdot_internal": true, "specdot_bounded": true, "specdot_type_kind": true, "specdot_base": true,
	"specdot_send": true, "specdot_receive": true,
	"specdot_key_type": true, "specdot_value_type": true, "specdot_values": true,
	"specdot_mutable": true, "specdot_identity": true, "specdot_authority": true, "specdot_lifecycle": true,
	"specdot_encoding": true, "specdot_version_type": true, "specdot_migration": true, "specdot_secret": true,
	"specdot_exclusive": true, "specdot_retention": true, "specdot_physical_id_type": true,
	"specdot_logical_id_type": true, "specdot_logical_identity": true, "specdot_replica_of": true,
	"specdot_shared_ownership": true, "specdot_relation": true, "specdot_cardinality": true,
	"specdot_recursive": true, "specdot_delivery": true, "specdot_ordering": true, "specdot_duplicate": true,
	"specdot_loss": true, "specdot_capacity": true, "specdot_durability": true, "specdot_integrity": true,
	"specdot_timing": true, "specdot_partition": true, "specdot_operation": true, "specdot_outcomes": true,
	"specdot_retain_on": true, "specdot_delete_on": true, "specdot_idempotency": true,
	"specdot_correlation": true, "specdot_retry": true, "specdot_reconcile": true, "specdot_assessor": true,
	"specdot_selection": true,
}

func applyProfile(profile config.Profile, ds []diag.Diagnostic) []diag.Diagnostic {
	for i := range ds {
		if severity, ok := profile.Rules[ds[i].Rule]; ok {
			if ds[i].Waivable || severity == "error" {
				switch severity {
				case "error":
					ds[i].Severity = diag.SeverityError
				case "warning":
					ds[i].Severity = diag.SeverityWarning
				case "note":
					ds[i].Severity = diag.SeverityNote
				case "off":
					ds[i].Severity = "off"
				}
			}
		}
		if profile.WarningsAsError && ds[i].Severity == diag.SeverityWarning {
			ds[i].Severity = diag.SeverityError
		}
	}
	result := ds[:0]
	for _, d := range ds {
		if d.Severity != "off" {
			result = append(result, d)
		}
	}
	return result
}

var nonWaivable = map[string]bool{
	"SDV001": true, "SDV010": true, "SDV020": true, "SDV022": true, "SDV023": true,
	"SDV051": true, "SDV052": true, "SDV053": true, "SDV101": true, "SDV104": true,
	"SDV211": true, "SDV520": true,
}

func applyWaivers(ctx Context, idx Index, ds []diag.Diagnostic) []diag.Diagnostic {
	type key struct{ id, rule string }
	valid := make(map[key]bool)
	critical := make(map[string]bool, len(nonWaivable))
	for rule := range nonWaivable {
		critical[rule] = true
	}
	for _, d := range ds {
		if !d.Waivable {
			critical[d.Rule] = true
		}
	}
	var extra []diag.Diagnostic
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		for _, waiver := range contract.Waivers {
			loc := contract.Location
			if critical[waiver.Rule] {
				extra = append(extra, diagnostic("SDV130", diag.SeverityError, loc, fmt.Sprintf("rule %s is safety-critical and cannot be waived", waiver.Rule), "fix the underlying defect", contract.For, false))
				continue
			}
			if waiver.Rule == "" || waiver.Reason == "" || waiver.Owner == "" || waiver.Expires == "" || waiver.Risk == "" || waiver.Mitigation == "" {
				extra = append(extra, diagnostic("SDV131", diag.SeverityError, loc, "waiver is missing rule, reason, owner, expires, risk, or mitigation", "waivers are reviewable risk decisions, not comments", contract.For, false))
				continue
			}
			expires, err := time.Parse("2006-01-02", waiver.Expires)
			if err != nil {
				extra = append(extra, diagnostic("SDV132", diag.SeverityError, loc, fmt.Sprintf("waiver expiry %q is not YYYY-MM-DD", waiver.Expires), "use an explicit calendar date", contract.For, false))
				continue
			}
			if expires.Before(ctx.Now.Truncate(24 * time.Hour)) {
				extra = append(extra, diagnostic("SDV133", diag.SeverityError, loc, fmt.Sprintf("waiver for %s expired on %s", waiver.Rule, waiver.Expires), "fix the issue or make a new risk decision", contract.For, false))
				continue
			}
			valid[key{contract.For, waiver.Rule}] = true
		}
	}
	result := make([]diag.Diagnostic, 0, len(ds)+len(extra))
	for _, d := range ds {
		if d.Waivable && valid[key{d.ElementID, d.Rule}] {
			continue
		}
		result = append(result, d)
	}
	return append(result, extra...)
}

func diagnostic(rule string, severity diag.Severity, loc model.Location, message, help, id string, waivable bool) diag.Diagnostic {
	return diag.Diagnostic{Rule: rule, Severity: severity, Location: toDiag(loc), Message: message, Help: help, ElementID: id, Waivable: waivable}
}

func toDiag(loc model.Location) diag.Location {
	return diag.Location{File: loc.File, Line: loc.Line, Column: loc.Column}
}

func split(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func truthy(value string) bool {
	return value == "true" || value == "yes" || value == "1"
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
