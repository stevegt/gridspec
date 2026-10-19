package rules

import (
	"fmt"
	"strings"

	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
)

func checkData(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	logical := make(map[string]model.Element)
	replicas := make(map[string]bool)
	for _, graph := range ctx.IR.Graphs {
		if graph.Kind != "data" {
			continue
		}
		for _, relationship := range graph.Edges {
			if relationship.Attrs["specdot_relation"] == "replica_of" {
				replicas[relationship.From], replicas[relationship.To] = true, true
			}
		}
	}
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		if contract.Data == nil {
			continue
		}
		data := *contract.Data
		element := idx.Elements[id]
		loc := contract.Location
		if loc.File == "" {
			loc = element.Location
		}
		if element.Kind != "type" && element.Kind != "resource" && element.Kind != "data" {
			ds = append(ds, diagnostic("SDV424", diag.SeverityError, loc, "data contract does not extend a type/resource declaration", "set an appropriate specdot_kind in a data graph", id, false))
		}
		validKind := map[string]bool{"scalar": true, "enum": true, "record": true, "union": true, "optional": true, "list": true, "set": true, "map": true, "ref": true, "newtype": true, "resource": true}
		if !validKind[data.Type] {
			ds = append(ds, diagnostic("SDV400", diag.SeverityError, loc, fmt.Sprintf("invalid or missing data type kind %q", data.Type), "use scalar, enum, record, union, optional, list, set, map, ref, newtype, or resource", id, false))
		}
		if data.Base != "" && !knownType(data.Base, idx) {
			ds = append(ds, diagnostic("SDV401", diag.SeverityError, loc, fmt.Sprintf("base type %q does not exist", data.Base), "reference a nominal type", id, false))
		}
		if (data.Type == "newtype" || data.Type == "optional" || data.Type == "list" || data.Type == "set" || data.Type == "ref") && data.Base == "" {
			ds = append(ds, diagnostic("SDV425", diag.SeverityError, loc, fmt.Sprintf("%s has no base element type", data.Type), "set data.base or specdot_base", id, false))
		}
		if data.Type == "map" {
			if !knownType(data.KeyType, idx) || !knownType(data.ValueType, idx) {
				ds = append(ds, diagnostic("SDV426", diag.SeverityError, loc, "map needs declared key_type and value_type", "reference nominal or primitive types", id, false))
			}
		}
		if data.Type == "enum" {
			seenValues := make(map[string]bool)
			if len(data.Values) == 0 {
				ds = append(ds, diagnostic("SDV427", diag.SeverityError, loc, "enum has no values", "declare the closed value set", id, false))
			}
			for _, value := range data.Values {
				if value == "" || seenValues[value] {
					ds = append(ds, diagnostic("SDV428", diag.SeverityError, loc, fmt.Sprintf("enum value %q is empty or duplicated", value), "use unique stable values", id, false))
				}
				seenValues[value] = true
			}
		}
		fields := make(map[string]bool)
		for _, field := range data.Fields {
			if field.Name == "" || fields[field.Name] {
				ds = append(ds, diagnostic("SDV402", diag.SeverityError, loc, fmt.Sprintf("field name %q is empty or duplicated", field.Name), "give record fields unique stable names", id, false))
			}
			fields[field.Name] = true
			if !knownType(field.Type, idx) {
				ds = append(ds, diagnostic("SDV403", diag.SeverityError, loc, fmt.Sprintf("field %q references unknown type %q", field.Name, field.Type), "declare the nominal type", id, false))
			}
			if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
				ds = append(ds, diagnostic("SDV404", diag.SeverityError, loc, fmt.Sprintf("field %q has min greater than max", field.Name), "correct cardinality", id, false))
			}
		}
		if data.Type == "record" && len(data.Fields) == 0 {
			ds = append(ds, diagnostic("SDV405", diag.SeverityError, loc, "record has no fields", "declare its structure", id, true))
		}
		if data.Type == "union" && len(data.Variants) < 2 {
			ds = append(ds, diagnostic("SDV406", diag.SeverityError, loc, "union has fewer than two variants", "declare all alternatives", id, false))
		}
		for _, variant := range data.Variants {
			if !knownType(variant, idx) {
				ds = append(ds, diagnostic("SDV407", diag.SeverityError, loc, fmt.Sprintf("union variant %q is unknown", variant), "declare the nominal variant type", id, false))
			}
		}
		if data.Mutable != nil && *data.Mutable && len(data.Identity) == 0 {
			ds = append(ds, diagnostic("SDV408", diag.SeverityError, loc, "mutable data has no identity fields", "declare stable logical identity", id, false))
		}
		if data.Exclusive && data.Authority == "" {
			ds = append(ds, diagnostic("SDV409", diag.SeverityError, loc, "exclusive resource has no authority agent", "name the agent controlling local mutation", id, false))
		}
		if data.Authority != "" {
			authority, ok := idx.Elements[data.Authority]
			if !ok || (authority.Kind != "agent" && authority.Kind != "superagent") {
				ds = append(ds, diagnostic("SDV416", diag.SeverityError, loc, fmt.Sprintf("data authority %q is not a declared agent", data.Authority), "reference the agent controlling local mutation", id, false))
			}
		}
		lowerAuthority := strings.ToLower(data.Authority)
		if lowerAuthority == "observed" || lowerAuthority == "copy" || lowerAuthority == "signature" {
			ds = append(ds, diagnostic("SDV410", diag.SeverityError, loc, fmt.Sprintf("%q is evidence, not an authority agent", data.Authority), "reference an agent that controls the resource", id, false))
		}
		logicalIDType := element.Attrs["specdot_logical_id_type"]
		if data.PhysicalIDType != "" && (data.PhysicalIDType == id || (logicalIDType != "" && data.PhysicalIDType == logicalIDType)) {
			ds = append(ds, diagnostic("SDV411", diag.SeverityError, loc, "physical file identity reuses the logical artifact identity type", "define a distinct nominal physical identity type", id, false))
		} else if data.PhysicalIDType != "" && !knownType(data.PhysicalIDType, idx) {
			ds = append(ds, diagnostic("SDV412", diag.SeverityError, loc, fmt.Sprintf("physical identity type %q does not exist", data.PhysicalIDType), "declare a distinct nominal type", id, false))
		}
		if key := element.Attrs["specdot_logical_identity"]; key != "" {
			if first, exists := logical[key]; exists && element.Attrs["specdot_replica_of"] == "" && first.Attrs["specdot_replica_of"] == "" && !replicas[element.ID] && !replicas[first.ID] {
				d := diagnostic("SDV413", diag.SeverityError, element.Location, fmt.Sprintf("two active resources claim logical identity %q without a replica relationship", key), "model one source of truth and explicit replicas, or use different identities", id, false)
				d.Related = []diag.RelatedLocation{{Location: toDiag(first.Location), Message: "other claimant"}}
				ds = append(ds, d)
			} else {
				logical[key] = element
			}
		}
		if ctx.Profile.BuildReady {
			if (data.Type == "record" || data.Type == "resource") && data.Encoding == "" && !latitudeAllows(contract, "encoding") {
				ds = append(ds, diagnostic("SDV414", diag.SeverityError, loc, "build-ready data has no encoding decision", "set data.encoding or declare constrained latitude", id, true))
			}
			if data.Version != "" && data.Migration == "" && !latitudeAllows(contract, "migration") {
				ds = append(ds, diagnostic("SDV415", diag.SeverityError, loc, "versioned data has no migration policy", "set migration or constrained latitude", id, true))
			}
		}
	}
	ds = append(ds, checkDataRelationships(ctx, idx)...)
	return ds
}

func checkDataRelationships(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	validRelation := map[string]bool{"contains": true, "references": true, "owns": true, "derives": true, "maps_to": true, "variant_of": true, "replica_of": true}
	validCardinality := map[string]bool{"1": true, "0..1": true, "1..*": true, "0..*": true}
	containment := make(map[string][]model.Element)
	owners := make(map[string][]model.Element)
	for _, graph := range ctx.IR.Graphs {
		if graph.Kind != "data" {
			continue
		}
		for _, relationship := range graph.Edges {
			if relationship.Kind != "relationship" {
				ds = append(ds, diagnostic("SDV417", diag.SeverityError, relationship.Location, fmt.Sprintf("data edge kind %q is not relationship", relationship.Kind), "set specdot_kind=relationship", relationship.ID, false))
			}
			for _, endpoint := range []string{relationship.From, relationship.To} {
				target, ok := idx.Elements[endpoint]
				if !ok || (target.Kind != "type" && target.Kind != "resource" && target.Kind != "data") {
					ds = append(ds, diagnostic("SDV418", diag.SeverityError, relationship.Location, fmt.Sprintf("relationship endpoint %q is not a data declaration", endpoint), "connect nominal types or resources", relationship.ID, false))
				}
			}
			relation := relationship.Attrs["specdot_relation"]
			if !validRelation[relation] {
				ds = append(ds, diagnostic("SDV419", diag.SeverityError, relationship.Location, fmt.Sprintf("unknown or missing data relation %q", relation), "use contains, references, owns, derives, maps_to, variant_of, or replica_of", relationship.ID, false))
			}
			cardinality := relationship.Attrs["specdot_cardinality"]
			if !validCardinality[cardinality] {
				ds = append(ds, diagnostic("SDV420", diag.SeverityError, relationship.Location, fmt.Sprintf("unknown or missing target cardinality %q", cardinality), "use 1, 0..1, 1..*, or 0..*", relationship.ID, false))
			}
			if relation == "contains" || relation == "owns" {
				containment[relationship.From] = append(containment[relationship.From], relationship)
			}
			if relation == "owns" {
				owners[relationship.To] = append(owners[relationship.To], relationship)
			}
			if relation == "references" && relationship.From == relationship.To && !truthy(relationship.Attrs["specdot_recursive"]) {
				ds = append(ds, diagnostic("SDV421", diag.SeverityError, relationship.Location, "self-recursive reference is not marked recursive", "set specdot_recursive=true and give the recursion a finite/base case in the data contract", relationship.ID, false))
			}
		}
	}
	for target, relationships := range owners {
		if len(relationships) > 1 && !truthy(idx.Elements[target].Attrs["specdot_shared_ownership"]) {
			ds = append(ds, diagnostic("SDV422", diag.SeverityError, relationships[1].Location, fmt.Sprintf("data %q has multiple owning parents", target), "choose one owner or explicitly model shared ownership", relationships[1].ID, false))
		}
	}
	state := make(map[string]int)
	var visit func(string)
	visit = func(id string) {
		state[id] = 1
		for _, relationship := range containment[id] {
			if state[relationship.To] == 1 {
				ds = append(ds, diagnostic("SDV423", diag.SeverityError, relationship.Location, "containment/ownership relationship forms a cycle", "use references for back-links; containment must be acyclic", relationship.ID, false))
			} else if state[relationship.To] == 0 {
				visit(relationship.To)
			}
		}
		state[id] = 2
	}
	for _, id := range sortedKeys(containment) {
		if state[id] == 0 {
			visit(id)
		}
	}
	return ds
}

func latitudeAllows(contract model.Contract, area string) bool {
	for _, latitude := range contract.Latitude {
		if latitude.Area == area && len(latitude.Allowed) > 0 {
			return true
		}
	}
	return false
}
