// Package specstate materializes immutable GridSpec semantic snapshots.
package specstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/refset"
	"github.com/promisegrid/gridspec/internal/source"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

const (
	EntityProtocolCID = "bafkreiex4wu4lzokpmgcbgaepcs4wyxrk3u2qwg3v522otvqn6w55vehja"
	StateProtocolCID  = "bafkreihmzn253eula4alvnya6tnt4o3s3aqzdjgjvn7qhess57u5sul2vq"
)

type Entity struct {
	Kind   string
	ID     string
	Fields map[string]any
	CID    cid.Cid
}

type State struct {
	Module   string
	Profile  string
	Entities map[string]Entity
	CID      cid.Cid
	closure  []cid.Cid
}

func Key(kind, id string) string { return kind + "\x00" + id }

func SplitKey(key string) (string, string, error) {
	position := strings.IndexByte(key, 0)
	if position < 1 || position == len(key)-1 || strings.IndexByte(key[position+1:], 0) >= 0 {
		return "", "", fmt.Errorf("invalid entity key")
	}
	return key[:position], key[position+1:], nil
}

func Empty(module, profile string) State {
	return State{Module: module, Profile: profile, Entities: make(map[string]Entity)}
}

func (state State) Clone() State {
	result := State{Module: state.Module, Profile: state.Profile, CID: state.CID, closure: append([]cid.Cid(nil), state.closure...), Entities: make(map[string]Entity, len(state.Entities))}
	for key, entity := range state.Entities {
		result.Entities[key] = Entity{Kind: entity.Kind, ID: entity.ID, Fields: cloneMap(entity.Fields), CID: entity.CID}
	}
	return result
}

func (state State) Closure() []cid.Cid { return append([]cid.Cid(nil), state.closure...) }

func Apply(base State, program txnlang.Program) (State, error) {
	result := base.Clone()
	result.CID = cid.Undef
	result.closure = nil
	if result.Entities == nil {
		result.Entities = make(map[string]Entity)
	}
	if program.Module != nil {
		result.Module = *program.Module
	}
	if program.Profile != nil {
		result.Profile = *program.Profile
	}
	for _, operation := range program.Ops {
		key := Key(operation.Kind, operation.ID)
		current, exists := result.Entities[key]
		switch operation.Action {
		case "put":
			fields, err := normalizeMap(operation.Fields)
			if err != nil {
				return State{}, fmt.Errorf("line %d put %s %s: %w", operation.Line, operation.Kind, operation.ID, err)
			}
			result.Entities[key] = Entity{Kind: operation.Kind, ID: operation.ID, Fields: fields}
		case "delete":
			if !exists {
				return State{}, fmt.Errorf("line %d: cannot delete absent %s %s", operation.Line, operation.Kind, operation.ID)
			}
			delete(result.Entities, key)
		case "assert_exists":
			if !exists {
				return State{}, fmt.Errorf("line %d: expected %s %s to exist", operation.Line, operation.Kind, operation.ID)
			}
		case "assert_absent":
			if exists {
				return State{}, fmt.Errorf("line %d: expected %s %s to be absent", operation.Line, operation.Kind, operation.ID)
			}
		case "assert_equals":
			if !exists {
				return State{}, fmt.Errorf("line %d: expected %s %s to exist", operation.Line, operation.Kind, operation.ID)
			}
			expected, err := normalizeValue(operation.Value)
			if err != nil {
				return State{}, err
			}
			actual, ok := current.Fields[operation.Field]
			if !ok || !reflect.DeepEqual(actual, expected) {
				return State{}, fmt.Errorf("line %d: %s %s field %s does not equal asserted value", operation.Line, operation.Kind, operation.ID, operation.Field)
			}
		default:
			return State{}, fmt.Errorf("unsupported transaction action %q", operation.Action)
		}
	}
	if result.Module == "" {
		return State{}, fmt.Errorf("resulting state has no module")
	}
	if result.Profile == "" {
		result.Profile = "default"
	}
	return result, nil
}

func Build(ctx context.Context, store cas.Store, state State) (State, error) {
	if store == nil || state.Module == "" || state.Profile == "" {
		return State{}, fmt.Errorf("store, module, and profile are required")
	}
	values := make(map[string][]byte, len(state.Entities))
	entityCIDs := make([]cid.Cid, 0, len(state.Entities))
	for key, candidate := range state.Entities {
		kind, id, err := SplitKey(key)
		if err != nil || kind != candidate.Kind || id != candidate.ID {
			return State{}, fmt.Errorf("entity map key does not match %s %s", candidate.Kind, candidate.ID)
		}
		encoded, entityCID, err := buildEntity(candidate)
		if err != nil {
			return State{}, err
		}
		if err := store.Put(ctx, entityCID, encoded); err != nil {
			return State{}, err
		}
		link, err := gridwire.EncodeCID(entityCID)
		if err != nil {
			return State{}, err
		}
		values[key] = link
		candidate.CID = entityCID
		state.Entities[key] = candidate
		entityCIDs = append(entityCIDs, entityCID)
	}
	entities, err := refset.Build(ctx, store, values)
	if err != nil {
		return State{}, fmt.Errorf("build entity reference set: %w", err)
	}
	module, _ := gridwire.Encode(state.Module)
	profile, _ := gridwire.Encode(state.Profile)
	root, _ := gridwire.EncodeCID(entities.Root())
	encoded, err := gridwire.BuildEnvelope(gridwire.ParseProtocolCID(StateProtocolCID), module, profile, root)
	if err != nil {
		return State{}, err
	}
	stateCID, err := gridwire.GridCID(encoded)
	if err != nil {
		return State{}, err
	}
	if err := store.Put(ctx, stateCID, encoded); err != nil {
		return State{}, err
	}
	closure := append([]cid.Cid{stateCID}, entities.Closure()...)
	closure = append(closure, entityCIDs...)
	state.CID, state.closure = stateCID, uniqueSorted(closure)
	return state, nil
}

func buildEntity(entity Entity) ([]byte, cid.Cid, error) {
	if entity.Kind == "" || entity.ID == "" || strings.IndexByte(entity.Kind, 0) >= 0 || strings.IndexByte(entity.ID, 0) >= 0 {
		return nil, cid.Undef, fmt.Errorf("entity kind and id must be nonempty and contain no NUL")
	}
	fields, err := normalizeMap(entity.Fields)
	if err != nil {
		return nil, cid.Undef, err
	}
	kind, _ := gridwire.Encode(entity.Kind)
	id, _ := gridwire.Encode(entity.ID)
	fieldBytes, err := gridwire.Encode(fields)
	if err != nil {
		return nil, cid.Undef, err
	}
	encoded, err := gridwire.BuildEnvelope(gridwire.ParseProtocolCID(EntityProtocolCID), kind, id, fieldBytes)
	if err != nil {
		return nil, cid.Undef, err
	}
	entityCID, err := gridwire.GridCID(encoded)
	return encoded, entityCID, err
}

func Load(ctx context.Context, store cas.Store, stateCID cid.Cid) (State, error) {
	data, err := exact(ctx, store, stateCID)
	if err != nil {
		return State{}, err
	}
	envelope, err := gridwire.ParseEnvelope(data)
	if err != nil || envelope.Protocol.String() != StateProtocolCID || len(envelope.Slots) != 3 {
		return State{}, fmt.Errorf("state must use the closed GridSpec state v1 protocol")
	}
	var module, profile string
	if err := gridwire.Canonical(envelope.Slots[0], &module); err != nil || module == "" {
		return State{}, fmt.Errorf("invalid state module")
	}
	if err := gridwire.Canonical(envelope.Slots[1], &profile); err != nil || profile == "" {
		return State{}, fmt.Errorf("invalid state profile")
	}
	root, err := gridwire.DecodeCID(envelope.Slots[2])
	if err != nil {
		return State{}, err
	}
	entities, err := refset.Open(ctx, store, root)
	if err != nil {
		return State{}, err
	}
	result := State{Module: module, Profile: profile, CID: stateCID, Entities: make(map[string]Entity)}
	closure := append([]cid.Cid{stateCID}, entities.Closure()...)
	err = entities.Scan(func(key, value []byte) error {
		kind, id, err := SplitKey(string(key))
		if err != nil {
			return err
		}
		entityCID, err := gridwire.DecodeCID(value)
		if err != nil {
			return err
		}
		entity, err := loadEntity(ctx, store, entityCID)
		if err != nil {
			return err
		}
		if entity.Kind != kind || entity.ID != id {
			return fmt.Errorf("reference-set key does not match entity %s", entityCID)
		}
		result.Entities[string(key)] = entity
		closure = append(closure, entityCID)
		return nil
	})
	if err != nil {
		return State{}, err
	}
	result.closure = uniqueSorted(closure)
	return result, nil
}

func loadEntity(ctx context.Context, store cas.Store, entityCID cid.Cid) (Entity, error) {
	data, err := exact(ctx, store, entityCID)
	if err != nil {
		return Entity{}, err
	}
	envelope, err := gridwire.ParseEnvelope(data)
	if err != nil || envelope.Protocol.String() != EntityProtocolCID || len(envelope.Slots) != 3 {
		return Entity{}, fmt.Errorf("entity %s has unsupported protocol", entityCID)
	}
	var kind, id string
	var fields map[string]any
	if err := gridwire.Canonical(envelope.Slots[0], &kind); err != nil || kind == "" {
		return Entity{}, fmt.Errorf("invalid entity kind")
	}
	if err := gridwire.Canonical(envelope.Slots[1], &id); err != nil || id == "" {
		return Entity{}, fmt.Errorf("invalid entity id")
	}
	if err := gridwire.Canonical(envelope.Slots[2], &fields); err != nil {
		return Entity{}, fmt.Errorf("invalid entity fields: %w", err)
	}
	fields, err = normalizeMap(fields)
	if err != nil {
		return Entity{}, err
	}
	return Entity{Kind: kind, ID: id, Fields: fields, CID: entityCID}, nil
}

func exact(ctx context.Context, store cas.Store, blockCID cid.Cid) ([]byte, error) {
	data, err := store.Get(ctx, blockCID)
	if err != nil {
		return nil, err
	}
	recomputed, err := blockCID.Prefix().Sum(data)
	if err != nil || !recomputed.Equals(blockCID) {
		return nil, fmt.Errorf("block bytes do not match CID %s", blockCID)
	}
	return data, nil
}

func uniqueSorted(values []cid.Cid) []cid.Cid {
	byText := make(map[string]cid.Cid, len(values))
	for _, value := range values {
		if value.Defined() {
			byText[value.String()] = value
		}
	}
	result := make([]cid.Cid, 0, len(byText))
	for _, value := range byText {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return bytes.Compare(result[i].Bytes(), result[j].Bytes()) < 0 })
	return result
}

func normalizeMap(input map[string]any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if key == "" {
			return nil, fmt.Errorf("field names must be nonempty")
		}
		normalized, err := normalizeValue(value)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", key, err)
		}
		result[key] = normalized
	}
	return result, nil
}

func normalizeValue(value any) (any, error) {
	switch current := value.(type) {
	case nil, bool, string, []byte, int64:
		return current, nil
	case int:
		return int64(current), nil
	case uint64:
		if current > uint64(^uint64(0)>>1) {
			return nil, fmt.Errorf("unsigned integer exceeds signed 64-bit range")
		}
		return int64(current), nil
	case json.Number:
		return current.Int64()
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			normalized, err := normalizeValue(item)
			if err != nil {
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	case map[string]any:
		return normalizeMap(current)
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func cloneMap(input map[string]any) map[string]any {
	result, err := normalizeMap(input)
	if err != nil {
		panic(err)
	}
	return result
}

// FieldsFrom converts an ordinary JSON-tagged Go value into transaction fields
// without permitting floating-point or implementation-specific values.
func FieldsFrom(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	return normalizeMap(fields)
}

func decodeFields(fields map[string]any, destination any) error {
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

// SemanticView adapts canonical state to the existing semantic checker.
func (state State) SemanticView() (model.IR, map[string]source.Document, []model.Contract, error) {
	ir := model.IR{Schema: model.IRSchema, Module: state.Module, Profile: state.Profile}
	documents := make(map[string]source.Document)
	graphs := make(map[string]*model.Graph)
	var pendingElements []model.Element
	keys := make([]string, 0, len(state.Entities))
	for key := range state.Entities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entity := state.Entities[key]
		switch entity.Kind {
		case "graph":
			var graph model.Graph
			if err := decodeFields(entity.Fields, &graph); err != nil {
				return model.IR{}, nil, nil, fmt.Errorf("decode graph %s: %w", entity.ID, err)
			}
			graph.ID = entity.ID
			graph.Nodes, graph.Edges = nil, nil
			defaultLocation(&graph.Location, "graph", entity.ID)
			copy := graph
			graphs[entity.ID] = &copy
		case "node", "edge":
			var element model.Element
			if err := decodeFields(entity.Fields, &element); err != nil {
				return model.IR{}, nil, nil, fmt.Errorf("decode %s %s: %w", entity.Kind, entity.ID, err)
			}
			element.ID, element.Element = entity.ID, entity.Kind
			defaultLocation(&element.Location, entity.Kind, entity.ID)
			pendingElements = append(pendingElements, element)
		case "contract":
			var contract model.Contract
			if err := decodeFields(entity.Fields, &contract); err != nil {
				return model.IR{}, nil, nil, fmt.Errorf("decode contract %s: %w", entity.ID, err)
			}
			contract.For = entity.ID
			if contract.Schema == "" {
				contract.Schema = "gridspec.contract/v1"
			}
			defaultLocation(&contract.Location, "contract", entity.ID)
			ir.Contracts = append(ir.Contracts, contract)
		case "document":
			var document struct {
				Path    string         `json:"path"`
				Content string         `json:"content"`
				SHA256  string         `json:"sha256"`
				Anchors map[string]int `json:"anchors"`
				Links   []source.Link  `json:"links"`
			}
			if err := decodeFields(entity.Fields, &document); err != nil {
				return model.IR{}, nil, nil, fmt.Errorf("decode document %s: %w", entity.ID, err)
			}
			if document.Path == "" {
				document.Path = entity.ID + ".md"
			}
			if _, duplicate := documents[document.Path]; duplicate {
				return model.IR{}, nil, nil, fmt.Errorf("more than one document entity selects path %s", document.Path)
			}
			digest := sha256.Sum256([]byte(document.Content))
			if document.SHA256 == "" || document.SHA256 != hex.EncodeToString(digest[:]) {
				return model.IR{}, nil, nil, fmt.Errorf("document %s content does not match its SHA-256", entity.ID)
			}
			indexed := source.IndexMarkdown(document.Path, []byte(document.Content))
			if !reflect.DeepEqual(indexed.Anchors, document.Anchors) || !reflect.DeepEqual(indexed.Links, document.Links) {
				return model.IR{}, nil, nil, fmt.Errorf("document %s anchors or links do not match its content", entity.ID)
			}
			indexed.Contracts = nil
			documents[document.Path] = indexed
		}
	}
	for _, element := range pendingElements {
		graph := graphs[element.GraphID]
		if graph == nil {
			// Preserve the declaration so ordinary checker diagnostics can report
			// the missing graph through a synthetic container.
			graph = &model.Graph{ID: element.GraphID, Kind: "missing", Status: "active", Directed: true, Location: model.Location{File: "gridspec/missing/" + element.GraphID}}
			graphs[element.GraphID] = graph
		}
		if element.Element == "node" {
			graph.Nodes = append(graph.Nodes, element)
		} else {
			graph.Edges = append(graph.Edges, element)
		}
	}
	graphIDs := make([]string, 0, len(graphs))
	for id := range graphs {
		graphIDs = append(graphIDs, id)
	}
	sort.Strings(graphIDs)
	for _, id := range graphIDs {
		ir.Graphs = append(ir.Graphs, *graphs[id])
	}
	rawContracts := append([]model.Contract(nil), ir.Contracts...)
	if err := ir.Seal(); err != nil {
		return model.IR{}, nil, nil, err
	}
	return ir, documents, rawContracts, nil
}

func defaultLocation(location *model.Location, kind, id string) {
	if location.File == "" {
		location.File = "gridspec/" + kind + "/" + id
		location.Line, location.Column = 1, 1
	}
}
