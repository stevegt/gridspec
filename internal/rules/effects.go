package rules

import (
	"fmt"
	"strings"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
)

func checkEffectsAndChannels(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	providers := make(map[string][]string)
	providerLoc := make(map[string]model.Location)
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		loc := contract.Location
		if loc.File == "" {
			if element, ok := idx.Elements[id]; ok {
				loc = element.Location
			}
		}
		if channel := contract.Channel; channel != nil {
			if element, ok := idx.Elements[id]; !ok || element.Kind != "channel" {
				ds = append(ds, diagnostic("SDV506", diag.SeverityError, loc, "channel contract does not extend a channel edge", "set specdot_kind=channel on the owning edge", id, false))
			}
			if channel.From == "" || channel.To == "" || channel.Payload == "" {
				ds = append(ds, diagnostic("SDV500", diag.SeverityError, loc, "channel is missing endpoints or payload type", "declare from, to, and payload", id, false))
			}
			for _, agent := range []string{channel.From, channel.To} {
				if element, ok := idx.Elements[agent]; !ok || (element.Kind != "agent" && element.Kind != "superagent") {
					ds = append(ds, diagnostic("SDV501", diag.SeverityError, loc, fmt.Sprintf("channel endpoint %q is not an agent", agent), "reference declared agents", id, false))
				}
			}
			if !knownType(channel.Payload, idx) {
				ds = append(ds, diagnostic("SDV502", diag.SeverityError, loc, fmt.Sprintf("channel payload type %q is unknown", channel.Payload), "declare a nominal data type", id, false))
			}
			if channel.Capacity <= 0 {
				ds = append(ds, diagnostic("SDV507", diag.SeverityError, loc, "channel capacity must be a positive finite bound", "set channel.capacity or specdot_capacity", id, false))
			}
			for field, valueSet := range map[string]struct {
				value string
				valid map[string]bool
			}{
				"delivery":   {channel.Delivery, set("best_effort", "at_most_once", "at_least_once", "exactly_once")},
				"ordering":   {channel.Ordering, set("none", "fifo", "per_key", "causal", "total")},
				"duplicate":  {channel.Duplicate, set("possible", "suppressed", "rejected")},
				"loss":       {channel.Loss, set("possible", "none")},
				"durability": {channel.Durability, set("volatile", "durable")},
				"integrity":  {channel.Integrity, set("none", "checksum", "authenticated")},
				"timing":     {channel.Timing, set("synchronous", "asynchronous", "bounded")},
				"partition":  {channel.Partition, set("block", "fail", "buffer", "drop")},
				"restart":    {channel.Restart, set("preserve", "replay", "discard", "reconcile")},
			} {
				if valueSet.value != "" && !valueSet.valid[valueSet.value] {
					ds = append(ds, diagnostic("SDV504", diag.SeverityError, loc, fmt.Sprintf("channel %s value %q is not defined by v1", field, valueSet.value), "use a documented channel semantic value", id, false))
				}
			}
			if channel.Delivery == "exactly_once" && contract.Metadata["exactly_once_mechanism"] == "" {
				ds = append(ds, diagnostic("SDV505", diag.SeverityError, loc, "exactly_once delivery has no deduplication/commit mechanism", "name the correlation, durable deduplication, and commit boundary in metadata.exactly_once_mechanism", id, false))
			}
			if ctx.Profile.BuildReady {
				missing := missingChannelSemantics(*channel)
				if len(missing) > 0 {
					ds = append(ds, diagnostic("SDV503", diag.SeverityError, loc, "build-ready channel leaves semantics open: "+strings.Join(missing, ", "), "specify delivery, ordering, duplication, loss, capacity, durability, integrity, timing, partition, and restart", id, true))
				}
			}
		}
		if effect := contract.Effect; effect != nil {
			if effect.Agent == "" || effect.Operation == "" || effect.Resource == "" {
				ds = append(ds, diagnostic("SDV510", diag.SeverityError, loc, "effect is missing controlling agent, operation, or resource", "make side effects explicit", id, false))
			}
			if agent, ok := idx.Elements[effect.Agent]; !ok || (agent.Kind != "agent" && agent.Kind != "superagent") {
				ds = append(ds, diagnostic("SDV511", diag.SeverityError, loc, fmt.Sprintf("effect agent %q is not declared", effect.Agent), "reference the agent controlling the local capability", id, false))
			}
			resourceContract, resourceOK := idx.Contracts[effect.Resource]
			if !resourceOK || resourceContract.Data == nil {
				ds = append(ds, diagnostic("SDV512", diag.SeverityError, loc, fmt.Sprintf("effect resource %q is not a data resource", effect.Resource), "declare the affected resource in a data graph", id, false))
			}
			if len(effect.Outcomes) == 0 || !hasString(effect.Outcomes, "success") || !hasString(effect.Outcomes, "failure") || !hasString(effect.Outcomes, "unknown") {
				ds = append(ds, diagnostic("SDV513", diag.SeverityError, loc, "effect does not account for success, failure, and unknown outcomes", "model all three result classes", id, false))
			}
			destructive := destructiveOperation(effect.Operation)
			if destructive {
				missing := make([]string, 0, 5)
				if len(effect.RetainOn) == 0 && len(effect.DeleteOn) == 0 {
					missing = append(missing, "retention")
				}
				if effect.Idempotency == "" {
					missing = append(missing, "idempotency")
				}
				if effect.Correlation == "" {
					missing = append(missing, "correlation")
				}
				if effect.Retry == "" {
					missing = append(missing, "retry")
				}
				if effect.Reconcile == "" {
					missing = append(missing, "reconcile")
				}
				if effect.Evidence == "" || effect.Assessor == "" {
					missing = append(missing, "evidence/assessor")
				}
				if len(missing) > 0 {
					ds = append(ds, diagnostic("SDV514", diag.SeverityError, loc, "destructive effect is incomplete: "+strings.Join(missing, ", "), "state how partial, retried, and uncertain execution is recovered", id, false))
				}
			}
			if resourceOK && resourceContract.Data != nil {
				if resourceContract.Data.Exclusive && resourceContract.Data.Authority != "" && effect.Agent != resourceContract.Data.Authority && (publicationOperation(effect.Operation) || destructiveOperation(effect.Operation)) && resourceContract.Metadata["selection"] == "" && !latitudeAllows(resourceContract, "provider-selection") {
					ds = append(ds, diagnostic("SDV515", diag.SeverityError, loc, fmt.Sprintf("agent %q mutates exclusive resource %q controlled by %q", effect.Agent, effect.Resource, resourceContract.Data.Authority), "route mutation through the authority or model an explicit authority transfer", id, false))
				}
				for _, outcome := range effect.DeleteOn {
					if hasString(resourceContract.Data.Retention, outcome) {
						ds = append(ds, diagnostic("SDV520", diag.SeverityError, loc, fmt.Sprintf("effect deletes resource on %q but its data contract requires retention", outcome), "preserve the artifact or change the reviewed retention contract", id, false))
					}
				}
				if resourceContract.Data.Exclusive && publicationOperation(effect.Operation) {
					providers[effect.Resource] = append(providers[effect.Resource], effect.Agent)
					providerLoc[effect.Resource] = loc
				}
			}
		}
	}
	for _, resource := range sortedKeys(providers) {
		agents := providers[resource]
		unique := make(map[string]bool)
		for _, agent := range agents {
			unique[agent] = true
		}
		if len(unique) > 1 {
			contract := idx.Contracts[resource]
			if contract.Metadata["selection"] == "" && !latitudeAllows(contract, "provider-selection") {
				ds = append(ds, diagnostic("SDV521", diag.SeverityError, providerLoc[resource], fmt.Sprintf("exclusive resource %q has multiple active publishing agents: %s", resource, strings.Join(sortedKeys(unique), ", ")), "choose one authority or specify deterministic provider selection/failover", resource, false))
			}
		}
	}
	return ds
}

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func missingChannelSemantics(c model.ChannelContract) []string {
	var missing []string
	for name, value := range map[string]string{
		"delivery": c.Delivery, "ordering": c.Ordering, "duplicate": c.Duplicate,
		"loss": c.Loss, "durability": c.Durability, "integrity": c.Integrity,
		"timing": c.Timing, "partition": c.Partition, "restart": c.Restart,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if c.Capacity <= 0 {
		missing = append(missing, "capacity")
	}
	return missing
}

func destructiveOperation(operation string) bool {
	switch strings.ToLower(operation) {
	case "delete", "remove", "replace", "truncate", "overwrite", "cleanup", "move":
		return true
	default:
		return false
	}
}

func publicationOperation(operation string) bool {
	switch strings.ToLower(operation) {
	case "publish", "install", "write", "replace", "upload":
		return true
	default:
		return false
	}
}
