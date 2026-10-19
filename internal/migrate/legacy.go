// Package migrate converts legacy compiler results into primary GridSpec
// transactions.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/source"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

// LegacyProgram creates one deterministic replacement transaction. DOT and
// Markdown are provenance inputs; the resulting transaction is authoritative.
func LegacyProgram(module, profile string, ir model.IR, documents map[string]source.Document, base specstate.State) ([]byte, error) {
	operations := make([]txnlang.Operation, 0)
	add := func(kind, id string, value any) error {
		fields, err := specstate.FieldsFrom(value)
		if err != nil {
			return err
		}
		operations = append(operations, txnlang.Operation{Action: "put", Kind: kind, ID: id, Fields: fields})
		return nil
	}
	for _, graph := range ir.Graphs {
		container := graph
		container.Nodes, container.Edges = nil, nil
		if err := add("graph", graph.ID, container); err != nil {
			return nil, err
		}
		for _, node := range graph.Nodes {
			if err := add("node", node.ID, node); err != nil {
				return nil, err
			}
		}
		for _, edge := range graph.Edges {
			if err := add("edge", edge.ID, edge); err != nil {
				return nil, err
			}
		}
	}
	for _, contract := range ir.Contracts {
		if err := add("contract", contract.For, contract); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	existingDocuments := make(map[string]string)
	for _, entity := range base.Entities {
		if entity.Kind != "document" {
			continue
		}
		path, ok := entity.Fields["path"].(string)
		if !ok || path == "" {
			continue
		}
		if previous, duplicate := existingDocuments[path]; duplicate {
			return nil, fmt.Errorf("accepted state has duplicate document path %q in %s and %s", path, previous, entity.ID)
		}
		existingDocuments[path] = entity.ID
	}
	for _, path := range paths {
		document := documents[path]
		id := existingDocuments[path]
		if id == "" {
			digest := sha256.Sum256([]byte(path))
			id = "doc." + hex.EncodeToString(digest[:12])
		}
		value := struct {
			Path    string         `json:"path"`
			Content string         `json:"content"`
			SHA256  string         `json:"sha256"`
			Anchors map[string]int `json:"anchors"`
			Links   []source.Link  `json:"links"`
		}{Path: path, Content: document.Content, Anchors: document.Anchors, Links: document.Links}
		contentDigest := sha256.Sum256([]byte(document.Content))
		value.SHA256 = hex.EncodeToString(contentDigest[:])
		if err := add("document", id, value); err != nil {
			return nil, err
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		left, right := operations[i].Kind+"\x00"+operations[i].ID, operations[j].Kind+"\x00"+operations[j].ID
		return left < right
	})
	for _, operation := range operations {
		if !txnlang.IsIdentifier(operation.ID) {
			return nil, fmt.Errorf("legacy semantic ID %q cannot be represented by GridSpec transaction language v1", operation.ID)
		}
	}
	return txnlang.Format(txnlang.Program{Module: &module, Profile: &profile, Ops: operations})
}
