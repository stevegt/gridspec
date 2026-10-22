// Package specdiff compares two materialized GridSpec states without asking a
// model to reinterpret either side.
package specdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/promisegrid/gridspec/internal/specstate"
)

const Schema = "gridspec.diff/v1"

type Report struct {
	Schema        string  `json:"schema"`
	AcceptedState string  `json:"accepted_state"`
	ProposedState string  `json:"proposed_state"`
	Changes       []Entry `json:"changes"`
}

type Entry struct {
	Operation string          `json:"operation"`
	Path      string          `json:"path"`
	Before    json.RawMessage `json:"before,omitempty"`
	After     json.RawMessage `json:"after,omitempty"`
}

func Compare(accepted, proposed specstate.State) (Report, error) {
	report := Report{Schema: Schema, AcceptedState: accepted.CID.String(), ProposedState: proposed.CID.String()}
	compareValue(&report.Changes, "/module", accepted.Module, proposed.Module, true, true)
	compareValue(&report.Changes, "/profile", accepted.Profile, proposed.Profile, true, true)
	keys := make(map[string]bool, len(accepted.Entities)+len(proposed.Entities))
	for key := range accepted.Entities {
		keys[key] = true
	}
	for key := range proposed.Entities {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		before, beforeOK := accepted.Entities[key]
		after, afterOK := proposed.Entities[key]
		kind, id, err := specstate.SplitKey(key)
		if err != nil {
			return Report{}, err
		}
		base := "/entities/" + pointer(kind) + "/" + pointer(id)
		if !beforeOK || !afterOK {
			var left, right any
			if beforeOK {
				left = entityValue(before)
			}
			if afterOK {
				right = entityValue(after)
			}
			compareValue(&report.Changes, base, left, right, beforeOK, afterOK)
			continue
		}
		compareValue(&report.Changes, base+"/fields", before.Fields, after.Fields, true, true)
	}
	sort.Slice(report.Changes, func(i, j int) bool {
		if report.Changes[i].Path != report.Changes[j].Path {
			return report.Changes[i].Path < report.Changes[j].Path
		}
		return report.Changes[i].Operation < report.Changes[j].Operation
	})
	return report, nil
}

func entityValue(entity specstate.Entity) map[string]any {
	return map[string]any{"kind": entity.Kind, "id": entity.ID, "fields": entity.Fields}
}

func compareValue(changes *[]Entry, path string, before, after any, beforeOK, afterOK bool) {
	if !beforeOK {
		*changes = append(*changes, Entry{Operation: "add", Path: path, After: encode(after)})
		return
	}
	if !afterOK {
		*changes = append(*changes, Entry{Operation: "remove", Path: path, Before: encode(before)})
		return
	}
	leftMap, leftMapOK := before.(map[string]any)
	rightMap, rightMapOK := after.(map[string]any)
	if leftMapOK && rightMapOK {
		keys := make(map[string]bool, len(leftMap)+len(rightMap))
		for key := range leftMap {
			keys[key] = true
		}
		for key := range rightMap {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			left, lok := leftMap[key]
			right, rok := rightMap[key]
			compareValue(changes, path+"/"+pointer(key), left, right, lok, rok)
		}
		return
	}
	leftSlice, leftSliceOK := before.([]any)
	rightSlice, rightSliceOK := after.([]any)
	if leftSliceOK && rightSliceOK {
		maximum := len(leftSlice)
		if len(rightSlice) > maximum {
			maximum = len(rightSlice)
		}
		for i := 0; i < maximum; i++ {
			var left, right any
			lok, rok := i < len(leftSlice), i < len(rightSlice)
			if lok {
				left = leftSlice[i]
			}
			if rok {
				right = rightSlice[i]
			}
			compareValue(changes, path+"/"+strconv.Itoa(i), left, right, lok, rok)
		}
		return
	}
	if !reflect.DeepEqual(before, after) {
		*changes = append(*changes, Entry{Operation: "change", Path: path, Before: encode(before), After: encode(after)})
	}
}

func encode(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func pointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func Write(writer io.Writer, format string, report Report) error {
	switch format {
	case "", "text":
		for _, change := range report.Changes {
			switch change.Operation {
			case "add":
				if _, err := fmt.Fprintf(writer, "+ %s = %s\n", change.Path, compact(change.After)); err != nil {
					return err
				}
			case "remove":
				if _, err := fmt.Fprintf(writer, "- %s = %s\n", change.Path, compact(change.Before)); err != nil {
					return err
				}
			case "change":
				if _, err := fmt.Fprintf(writer, "~ %s: %s -> %s\n", change.Path, compact(change.Before), compact(change.After)); err != nil {
					return err
				}
			}
		}
		if len(report.Changes) == 0 {
			_, err := fmt.Fprintln(writer, "no semantic changes")
			return err
		}
		return nil
	case "json":
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	default:
		return fmt.Errorf("unknown diff format %q", format)
	}
}

func compact(value []byte) string {
	var output bytes.Buffer
	if json.Compact(&output, value) != nil {
		return string(value)
	}
	return output.String()
}
