package txnlang

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// Format emits a canonical, reparsable representation of a program. It is
// used for deterministic imports and disassembly proposals; exact user input
// is otherwise preserved as the transaction artifact.
func Format(program Program) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("gridspec 1\n")
	if program.Module != nil {
		fmt.Fprintf(&output, "module %s\n", strconv.Quote(*program.Module))
	}
	if program.Profile != nil {
		fmt.Fprintf(&output, "profile %s\n", strconv.Quote(*program.Profile))
	}
	for _, operation := range program.Ops {
		if !IsIdentifier(operation.Kind) || !IsIdentifier(operation.ID) {
			return nil, fmt.Errorf("kind and ID must be transaction identifiers: %q %q", operation.Kind, operation.ID)
		}
		switch operation.Action {
		case "put":
			fmt.Fprintf(&output, "put %s %s ", operation.Kind, operation.ID)
			if err := writeMap(&output, operation.Fields); err != nil {
				return nil, err
			}
			output.WriteByte('\n')
		case "delete":
			fmt.Fprintf(&output, "delete %s %s\n", operation.Kind, operation.ID)
		case "assert_exists":
			fmt.Fprintf(&output, "assert exists %s %s\n", operation.Kind, operation.ID)
		case "assert_absent":
			fmt.Fprintf(&output, "assert absent %s %s\n", operation.Kind, operation.ID)
		case "assert_equals":
			if !IsIdentifier(operation.Field) {
				return nil, fmt.Errorf("assert field must be an identifier: %q", operation.Field)
			}
			fmt.Fprintf(&output, "assert equals %s %s %s ", operation.Kind, operation.ID, operation.Field)
			if err := writeValue(&output, operation.Value); err != nil {
				return nil, err
			}
			output.WriteByte('\n')
		default:
			return nil, fmt.Errorf("unsupported transaction action %q", operation.Action)
		}
	}
	encoded := output.Bytes()
	if _, err := Parse(encoded); err != nil {
		return nil, fmt.Errorf("internal transaction formatter produced invalid program: %w", err)
	}
	return encoded, nil
}

func writeMap(output *bytes.Buffer, fields map[string]any) error {
	output.WriteByte('{')
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		if index != 0 {
			output.WriteByte(',')
		}
		output.WriteByte(' ')
		if IsIdentifier(key) {
			output.WriteString(key)
		} else {
			output.WriteString(strconv.Quote(key))
		}
		output.WriteString(" = ")
		if err := writeValue(output, fields[key]); err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}
	if len(keys) != 0 {
		output.WriteByte(' ')
	}
	output.WriteByte('}')
	return nil
}

func writeValue(output *bytes.Buffer, value any) error {
	switch current := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		output.WriteString(strconv.FormatBool(current))
	case string:
		output.WriteString(strconv.Quote(current))
	case int:
		output.WriteString(strconv.FormatInt(int64(current), 10))
	case int64:
		output.WriteString(strconv.FormatInt(current, 10))
	case []any:
		output.WriteByte('[')
		for index, item := range current {
			if index != 0 {
				output.WriteString(", ")
			}
			if err := writeValue(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		return writeMap(output, current)
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}
	return nil
}

// IsIdentifier reports whether value can appear as an unquoted transaction
// identifier under Version 1.
func IsIdentifier(value string) bool {
	if value == "" || !isIdentStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isIdentContinue(value[index]) {
			return false
		}
	}
	return true
}
