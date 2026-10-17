package source

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
	"github.com/yuin/goldmark"
	mdast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v4"
)

func parseMarkdown(f File) (Document, []diag.Diagnostic) {
	doc := Document{Path: f.Rel, Abs: f.Abs, Anchors: make(map[string]int)}
	idx := newLineIndex(f.Rel, f.Data)
	root := goldmark.New().Parser().Parse(text.NewReader(f.Data))
	var ds []diag.Diagnostic
	anchorCount := make(map[string]int)
	walkErr := mdast.Walk(root, func(node mdast.Node, entering bool) (mdast.WalkStatus, error) {
		if !entering {
			return mdast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *mdast.Heading:
			name := slugify(nodeText(n, f.Data))
			if name == "" {
				return mdast.WalkContinue, nil
			}
			anchorCount[name]++
			if anchorCount[name] > 1 {
				name = fmt.Sprintf("%s-%d", name, anchorCount[name]-1)
			}
			line := 1
			if n.Lines().Len() > 0 {
				line = idx.atOffset(n.Lines().At(0).Start).Line
			}
			doc.Anchors[name] = line
		case *mdast.Link:
			loc := idx.atOffset(firstNodeOffset(n))
			doc.Links = append(doc.Links, Link{Destination: string(n.Destination), Line: loc.Line})
		case *mdast.FencedCodeBlock:
			if string(n.Language(f.Data)) != "specdot" {
				return mdast.WalkContinue, nil
			}
			body := linesBytes(n.Lines(), f.Data)
			line := 1
			if n.Lines().Len() > 0 {
				line = idx.atOffset(n.Lines().At(0).Start).Line
			}
			contract, contractDS := decodeContract(body, model.Location{File: f.Rel, Line: line, Column: 1})
			ds = append(ds, contractDS...)
			if contract.For != "" {
				doc.Contracts = append(doc.Contracts, contract)
			}
		}
		return mdast.WalkContinue, nil
	})
	if walkErr != nil {
		ds = append(ds, diag.Diagnostic{Rule: "SDV050", Severity: diag.SeverityError, Message: walkErr.Error(), Location: diag.Location{File: f.Rel}, Waivable: false})
	}
	return doc, ds
}

func firstNodeOffset(node mdast.Node) int {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if n, ok := child.(*mdast.Text); ok {
			return n.Segment.Start
		}
		if offset := firstNodeOffset(child); offset != 0 {
			return offset
		}
	}
	return 0
}

func decodeContract(data []byte, loc model.Location) (model.Contract, []diag.Diagnostic) {
	where := diag.Location{File: loc.File, Line: loc.Line, Column: loc.Column}
	var raw yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return model.Contract{}, []diag.Diagnostic{{Rule: "SDV051", Severity: diag.SeverityError, Message: "invalid specdot YAML: " + err.Error(), Location: where, Waivable: false}}
	}
	if problem := forbiddenYAML(&raw); problem != "" {
		return model.Contract{}, []diag.Diagnostic{{Rule: "SDV052", Severity: diag.SeverityError, Message: problem, Location: where, Help: "use plain mappings and sequences without aliases, merge keys, or custom tags", Waivable: false}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var contract model.Contract
	if err := dec.Decode(&contract); err != nil {
		return model.Contract{}, []diag.Diagnostic{{Rule: "SDV053", Severity: diag.SeverityError, Message: "invalid specdot contract: " + err.Error(), Location: where, Waivable: false}}
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return model.Contract{}, []diag.Diagnostic{{Rule: "SDV053", Severity: diag.SeverityError, Message: "specdot block must contain exactly one YAML document", Location: where, Waivable: false}}
		}
		return model.Contract{}, []diag.Diagnostic{{Rule: "SDV053", Severity: diag.SeverityError, Message: "invalid trailing YAML: " + err.Error(), Location: where, Waivable: false}}
	}
	contract.Location = loc
	var ds []diag.Diagnostic
	if contract.Schema != "specdot.contract/v1" {
		ds = append(ds, diag.Diagnostic{Rule: "SDV054", Severity: diag.SeverityError, Message: fmt.Sprintf("unsupported contract schema %q", contract.Schema), Location: where, Help: "set schema: specdot.contract/v1", Waivable: false})
	}
	if contract.For == "" {
		ds = append(ds, diag.Diagnostic{Rule: "SDV055", Severity: diag.SeverityError, Message: "contract is missing for", Location: where, Help: "set for to the semantic id extended by this block", Waivable: false})
	}
	return contract, ds
}

func forbiddenYAML(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" || node.Alias != nil {
		return "YAML aliases and anchors are not allowed in specdot contracts"
	}
	if node.Value == "<<" {
		return "YAML merge keys are not allowed in specdot contracts"
	}
	if node.Tag != "" && !strings.HasPrefix(node.Tag, "!!") {
		return fmt.Sprintf("custom YAML tag %q is not allowed", node.Tag)
	}
	if node.Kind == yaml.MappingNode {
		keys := make(map[string]bool)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if keys[key] {
				return fmt.Sprintf("duplicate YAML key %q is not allowed", key)
			}
			keys[key] = true
		}
	}
	for _, child := range node.Content {
		if problem := forbiddenYAML(child); problem != "" {
			return problem
		}
	}
	return ""
}

func linesBytes(lines *text.Segments, source []byte) []byte {
	var b bytes.Buffer
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		b.Write(segment.Value(source))
	}
	return b.Bytes()
}

func nodeText(node mdast.Node, source []byte) string {
	var b strings.Builder
	_ = mdast.Walk(node, func(child mdast.Node, entering bool) (mdast.WalkStatus, error) {
		if entering {
			if textNode, ok := child.(*mdast.Text); ok {
				b.Write(textNode.Segment.Value(source))
			}
		}
		return mdast.WalkContinue, nil
	})
	return b.String()
}

var dashRun = regexp.MustCompile(`-+`)

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) {
			b.WriteByte('-')
		}
	}
	return strings.Trim(dashRun.ReplaceAllString(b.String(), "-"), "-")
}
