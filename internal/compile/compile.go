// Package compile links syntax trees and Markdown extensions into canonical IR.
package compile

import (
	"sort"
	"strconv"
	"strings"

	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/source"
)

// IR builds the effective contract set. Markdown contracts take precedence over
// equivalent DOT attributes; both remain traceable to source locations.
func IR(cfg config.Config, profile string, parsed source.Parsed) model.IR {
	ir := model.IR{
		Schema: model.IRSchema, Module: cfg.Module, Profile: profile, RootDir: cfg.Dir,
		Graphs: append([]model.Graph(nil), parsed.Graphs...),
	}
	for _, f := range parsed.Files {
		ir.Sources = append(ir.Sources, f.Source)
	}

	explicit := make(map[string]model.Contract)
	for _, contract := range parsed.Contracts {
		explicit[contract.For] = contract
	}
	seen := make(map[string]bool)
	for _, graph := range ir.Graphs {
		base := contractForGraph(graph)
		if ext, ok := explicit[graph.ID]; ok {
			base = mergeContract(base, ext)
		}
		if hasContractContent(base) {
			ir.Contracts = append(ir.Contracts, base)
			seen[graph.ID] = true
		}
		for _, element := range append(append([]model.Element(nil), graph.Nodes...), graph.Edges...) {
			base := contractForElement(element)
			if ext, ok := explicit[element.ID]; ok {
				base = mergeContract(base, ext)
			}
			if hasContractContent(base) {
				ir.Contracts = append(ir.Contracts, base)
				seen[element.ID] = true
			}
		}
	}
	explicitIDs := make([]string, 0, len(explicit))
	for id := range explicit {
		explicitIDs = append(explicitIDs, id)
	}
	sort.Strings(explicitIDs)
	for _, id := range explicitIDs {
		contract := explicit[id]
		if !seen[id] {
			ir.Contracts = append(ir.Contracts, contract)
		}
	}
	return ir
}

func contractForGraph(graph model.Graph) model.Contract {
	c := model.Contract{
		Schema: "gridspec.contract/v1", For: graph.ID, Kind: graph.Kind,
		Status: graph.Status, Location: graph.Location,
	}
	if graph.Kind == "machine" {
		c.Machine = &model.MachineContract{
			Agent:     graph.Attrs["specdot_agent"],
			Semantics: graph.Attrs["specdot_semantics"],
		}
		for _, event := range splitList(graph.Attrs["specdot_accepts"]) {
			c.Machine.Events = append(c.Machine.Events, model.EventContract{Name: event, Policy: "handled"})
		}
	}
	return c
}

func contractForElement(e model.Element) model.Contract {
	c := model.Contract{
		Schema: "gridspec.contract/v1", For: e.ID, Kind: e.Kind,
		Status: e.Status, Location: e.Location,
	}
	switch e.Kind {
	case "agent", "superagent":
		if e.Attrs["specdot_controls"] != "" || e.Attrs["specdot_capabilities"] != "" || e.Attrs["specdot_machines"] != "" || e.Attrs["specdot_members"] != "" {
			c.Agent = &model.AgentContract{
				Controls: splitList(e.Attrs["specdot_controls"]), Capabilities: splitList(e.Attrs["specdot_capabilities"]),
				Machines: splitList(e.Attrs["specdot_machines"]), Members: splitList(e.Attrs["specdot_members"]),
			}
		}
	case "promise":
		c.Promise = &model.PromiseContract{
			Promiser: e.Attrs["specdot_promiser"], Promisee: e.Attrs["specdot_promisee"],
			Polarity: e.Attrs["specdot_polarity"], External: boolValue(e.Attrs["specdot_external"]),
			Prerequisite: e.Attrs["specdot_prerequisite"], Condition: e.Attrs["specdot_condition"],
			Evidence: e.Attrs["specdot_evidence"], Scope: splitList(e.Attrs["specdot_scope"]),
			Body: model.Body{Type: e.Attrs["specdot_type"], Constraints: e.Attrs["specdot_constraints"], Resource: e.Attrs["specdot_resource"]},
		}
		if c.Promise.Promiser == "" {
			c.Promise.Promiser = e.From
		}
		if c.Promise.Promisee == "" {
			c.Promise.Promisee = e.To
		}
	case "signal", "imposition":
		c.Signal = &model.SignalContract{
			Sender: e.From, Recipient: e.To, Mode: e.Kind,
			Body:      model.Body{Type: e.Attrs["specdot_type"], Constraints: e.Attrs["specdot_constraints"], Resource: e.Attrs["specdot_resource"]},
			Condition: e.Attrs["specdot_condition"],
		}
	case "assessment":
		c.Assessment = &model.AssessmentContract{
			Observer: e.Attrs["specdot_observer"], Subject: e.Attrs["specdot_subject"],
			Predicate: e.Attrs["specdot_predicate"], Evidence: e.Attrs["specdot_evidence"],
		}
		if c.Assessment.Observer == "" {
			c.Assessment.Observer = e.From
		}
		if c.Assessment.Subject == "" {
			c.Assessment.Subject = e.To
		}
	case "type", "resource", "data":
		mutable, hasMutable := boolAttribute(e.Attrs, "specdot_mutable")
		c.Data = &model.DataContract{
			Type: e.Attrs["specdot_type_kind"], Base: e.Attrs["specdot_base"], KeyType: e.Attrs["specdot_key_type"], ValueType: e.Attrs["specdot_value_type"], Values: splitList(e.Attrs["specdot_values"]),
			Identity: splitList(e.Attrs["specdot_identity"]), Authority: e.Attrs["specdot_authority"],
			Lifecycle: splitList(e.Attrs["specdot_lifecycle"]), Encoding: e.Attrs["specdot_encoding"],
			Version: e.Attrs["specdot_version_type"], Migration: e.Attrs["specdot_migration"],
			Secret: boolValue(e.Attrs["specdot_secret"]), Exclusive: boolValue(e.Attrs["specdot_exclusive"]),
			Retention: splitList(e.Attrs["specdot_retention"]), PhysicalIDType: e.Attrs["specdot_physical_id_type"],
		}
		if hasMutable {
			c.Data.Mutable = &mutable
		}
	case "channel":
		capacity, _ := strconv.Atoi(e.Attrs["specdot_capacity"])
		c.Channel = &model.ChannelContract{
			From: e.From, To: e.To, Payload: e.Attrs["specdot_type"],
			Delivery: e.Attrs["specdot_delivery"], Ordering: e.Attrs["specdot_ordering"],
			Duplicate: e.Attrs["specdot_duplicate"], Loss: e.Attrs["specdot_loss"], Capacity: capacity,
			Durability: e.Attrs["specdot_durability"], Integrity: e.Attrs["specdot_integrity"],
			Timing: e.Attrs["specdot_timing"], Partition: e.Attrs["specdot_partition"], Restart: e.Attrs["specdot_restart"],
			External: boolValue(e.Attrs["specdot_external"]),
		}
	case "effect":
		c.Effect = effectFromAttrs(e.Attrs)
	}
	if e.Attrs["specdot_operation"] != "" && c.Effect == nil {
		c.Effect = effectFromAttrs(e.Attrs)
	}
	return c
}

func effectFromAttrs(attrs map[string]string) *model.EffectContract {
	return &model.EffectContract{
		Agent: attrs["specdot_agent"], Operation: attrs["specdot_operation"], Resource: attrs["specdot_resource"],
		Outcomes: splitList(attrs["specdot_outcomes"]), RetainOn: splitList(attrs["specdot_retain_on"]),
		DeleteOn: splitList(attrs["specdot_delete_on"]), Idempotency: attrs["specdot_idempotency"],
		Correlation: attrs["specdot_correlation"], Retry: attrs["specdot_retry"], Reconcile: attrs["specdot_reconcile"],
		Evidence: attrs["specdot_evidence"], Assessor: attrs["specdot_assessor"],
	}
}

func mergeContract(base, ext model.Contract) model.Contract {
	result := base
	result.Schema = ext.Schema
	result.For = ext.For
	if ext.Kind != "" {
		result.Kind = ext.Kind
	}
	if ext.Status != "" {
		result.Status = ext.Status
	}
	if ext.Summary != "" {
		result.Summary = ext.Summary
	}
	if ext.Promise != nil {
		result.Promise = ext.Promise
	}
	if ext.Agent != nil {
		result.Agent = ext.Agent
	}
	if ext.Signal != nil {
		result.Signal = ext.Signal
	}
	if ext.Assessment != nil {
		result.Assessment = ext.Assessment
	}
	if ext.Machine != nil {
		result.Machine = ext.Machine
	}
	if ext.Data != nil {
		result.Data = ext.Data
	}
	if ext.Channel != nil {
		result.Channel = ext.Channel
	}
	if ext.Effect != nil {
		result.Effect = ext.Effect
	}
	result.Claims = ext.Claims
	result.Waivers = ext.Waivers
	result.Latitude = ext.Latitude
	result.Metadata = ext.Metadata
	result.Location = ext.Location
	return result
}

func hasContractContent(c model.Contract) bool {
	return c.Agent != nil || c.Promise != nil || c.Signal != nil || c.Assessment != nil || c.Machine != nil || c.Data != nil || c.Channel != nil || c.Effect != nil ||
		len(c.Claims) != 0 || len(c.Waivers) != 0 || len(c.Latitude) != 0 || c.Summary != ""
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func boolValue(value string) bool {
	b, _ := strconv.ParseBool(value)
	return b
}

func boolAttribute(attrs map[string]string, key string) (bool, bool) {
	value, ok := attrs[key]
	if !ok {
		return false, false
	}
	return boolValue(value), true
}
