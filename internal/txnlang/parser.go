// Package txnlang parses the finite GridSpec transaction language.
package txnlang

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Program struct {
	Module  *string
	Profile *string
	Ops     []Operation
}

type Operation struct {
	Kind   string
	ID     string
	Action string
	Field  string
	Value  any
	Fields map[string]any
	Line   int
}

type tokenKind uint8

const (
	tEOF tokenKind = iota
	tIdent
	tString
	tInteger
	tLBrace
	tRBrace
	tLBracket
	tRBracket
	tEqual
	tComma
	tSemicolon
)

type token struct {
	kind tokenKind
	text string
	line int
	col  int
}

type lexer struct {
	source []byte
	offset int
	line   int
	col    int
}

func Parse(source []byte) (Program, error) {
	if !utf8.Valid(source) {
		return Program{}, fmt.Errorf("transaction program is not valid UTF-8")
	}
	if len(source) >= 3 && string(source[:3]) == "\xef\xbb\xbf" {
		return Program{}, fmt.Errorf("transaction program must not contain a byte-order mark")
	}
	if strings.ContainsRune(string(source), '\r') {
		return Program{}, fmt.Errorf("transaction program must use LF line endings")
	}
	parser := &parser{lex: lexer{source: source, line: 1, col: 1}}
	return parser.parse()
}

type parser struct {
	lex     lexer
	peeked  token
	hasPeek bool
}

func (p *parser) parse() (Program, error) {
	first, err := p.next()
	if err != nil {
		return Program{}, err
	}
	version, err := p.next()
	if err != nil {
		return Program{}, err
	}
	if first.kind != tIdent || first.text != "gridspec" || version.kind != tInteger || version.text != "1" {
		return Program{}, p.problem(first, "program must begin with 'gridspec 1'")
	}
	var result Program
	for {
		tok, err := p.nextMeaningful()
		if err != nil {
			return Program{}, err
		}
		if tok.kind == tEOF {
			return result, nil
		}
		if tok.kind != tIdent {
			return Program{}, p.problem(tok, "expected a transaction statement")
		}
		switch tok.text {
		case "module", "profile":
			value, err := p.require(tString, tok.text+" requires a quoted string")
			if err != nil {
				return Program{}, err
			}
			decoded, err := strconv.Unquote(value.text)
			if err != nil || decoded == "" {
				return Program{}, p.problem(value, tok.text+" must be a nonempty string")
			}
			if tok.text == "module" {
				if result.Module != nil {
					return Program{}, p.problem(tok, "module may appear only once")
				}
				result.Module = &decoded
			} else {
				if result.Profile != nil {
					return Program{}, p.problem(tok, "profile may appear only once")
				}
				result.Profile = &decoded
			}
		case "put":
			op, err := p.parsePut(tok.line)
			if err != nil {
				return Program{}, err
			}
			result.Ops = append(result.Ops, op)
		case "delete":
			kind, id, err := p.kindAndID()
			if err != nil {
				return Program{}, err
			}
			result.Ops = append(result.Ops, Operation{Action: "delete", Kind: kind, ID: id, Line: tok.line})
		case "assert":
			op, err := p.parseAssert(tok.line)
			if err != nil {
				return Program{}, err
			}
			result.Ops = append(result.Ops, op)
		default:
			return Program{}, p.problem(tok, fmt.Sprintf("unknown statement %q", tok.text))
		}
	}
}

func (p *parser) parsePut(line int) (Operation, error) {
	kind, id, err := p.kindAndID()
	if err != nil {
		return Operation{}, err
	}
	open, err := p.require(tLBrace, "put requires a field block")
	if err != nil {
		return Operation{}, err
	}
	fields, err := p.parseMap(open, tRBrace)
	if err != nil {
		return Operation{}, err
	}
	return Operation{Action: "put", Kind: kind, ID: id, Fields: fields, Line: line}, nil
}

func (p *parser) parseAssert(line int) (Operation, error) {
	mode, err := p.require(tIdent, "assert requires exists, absent, or equals")
	if err != nil {
		return Operation{}, err
	}
	if mode.text != "exists" && mode.text != "absent" && mode.text != "equals" {
		return Operation{}, p.problem(mode, "assert requires exists, absent, or equals")
	}
	kind, id, err := p.kindAndID()
	if err != nil {
		return Operation{}, err
	}
	op := Operation{Action: "assert_" + mode.text, Kind: kind, ID: id, Line: line}
	if mode.text == "equals" {
		field, err := p.require(tIdent, "assert equals requires a field")
		if err != nil {
			return Operation{}, err
		}
		value, err := p.parseValue()
		if err != nil {
			return Operation{}, err
		}
		op.Field, op.Value = field.text, value
	}
	return op, nil
}

func (p *parser) kindAndID() (string, string, error) {
	kind, err := p.require(tIdent, "entity kind is required")
	if err != nil {
		return "", "", err
	}
	id, err := p.require(tIdent, "entity id is required")
	if err != nil {
		return "", "", err
	}
	return kind.text, id.text, nil
}

func (p *parser) parseMap(open token, close tokenKind) (map[string]any, error) {
	result := make(map[string]any)
	for {
		tok, err := p.nextMeaningful()
		if err != nil {
			return nil, err
		}
		if tok.kind == close {
			return result, nil
		}
		if tok.kind != tIdent && tok.kind != tString {
			return nil, p.problem(tok, "map field name is required")
		}
		key := tok.text
		if tok.kind == tString {
			key, err = strconv.Unquote(tok.text)
			if err != nil {
				return nil, p.problem(tok, "invalid quoted map key")
			}
		}
		if key == "" {
			return nil, p.problem(tok, "map field name must be nonempty")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, p.problem(tok, fmt.Sprintf("field %q is duplicated", key))
		}
		if _, err := p.require(tEqual, "map field requires '='"); err != nil {
			return nil, err
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		result[key] = value
		separator, err := p.next()
		if err != nil {
			return nil, err
		}
		if separator.kind == close {
			return result, nil
		}
		if separator.kind == tComma || separator.kind == tSemicolon {
			continue
		}
		p.unread(separator)
	}
}

func (p *parser) parseValue() (any, error) {
	tok, err := p.nextMeaningful()
	if err != nil {
		return nil, err
	}
	switch tok.kind {
	case tString:
		value, err := strconv.Unquote(tok.text)
		if err != nil {
			return nil, p.problem(tok, "invalid string literal")
		}
		return value, nil
	case tInteger:
		value, err := strconv.ParseInt(tok.text, 10, 64)
		if err != nil {
			return nil, p.problem(tok, "integer is outside signed 64-bit range")
		}
		return value, nil
	case tIdent:
		switch tok.text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		default:
			return nil, p.problem(tok, "text values must be quoted")
		}
	case tLBracket:
		var values []any
		for {
			next, err := p.nextMeaningful()
			if err != nil {
				return nil, err
			}
			if next.kind == tRBracket {
				return values, nil
			}
			p.unread(next)
			value, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			values = append(values, value)
			separator, err := p.next()
			if err != nil {
				return nil, err
			}
			if separator.kind == tRBracket {
				return values, nil
			}
			if separator.kind != tComma && separator.kind != tSemicolon {
				return nil, p.problem(separator, "array values require ',' or ']'")
			}
		}
	case tLBrace:
		return p.parseMap(tok, tRBrace)
	default:
		return nil, p.problem(tok, "value is required")
	}
}

func (p *parser) nextMeaningful() (token, error) {
	for {
		tok, err := p.next()
		if err != nil {
			return token{}, err
		}
		if tok.kind != tSemicolon && tok.kind != tComma {
			return tok, nil
		}
	}
}

func (p *parser) require(kind tokenKind, message string) (token, error) {
	tok, err := p.nextMeaningful()
	if err != nil {
		return token{}, err
	}
	if tok.kind != kind {
		return token{}, p.problem(tok, message)
	}
	return tok, nil
}

func (p *parser) next() (token, error) {
	if p.hasPeek {
		p.hasPeek = false
		return p.peeked, nil
	}
	return p.lex.next()
}

func (p *parser) unread(tok token) { p.peeked, p.hasPeek = tok, true }

func (p *parser) problem(tok token, message string) error {
	return fmt.Errorf("transaction:%d:%d: %s", tok.line, tok.col, message)
}

func (l *lexer) next() (token, error) {
	for l.offset < len(l.source) {
		current := l.source[l.offset]
		if current == '#' {
			for l.offset < len(l.source) && l.source[l.offset] != '\n' {
				l.advanceByte()
			}
			continue
		}
		if unicode.IsSpace(rune(current)) {
			l.advanceByte()
			continue
		}
		break
	}
	if l.offset == len(l.source) {
		return token{kind: tEOF, line: l.line, col: l.col}, nil
	}
	start, line, col := l.offset, l.line, l.col
	current := l.source[l.offset]
	single := map[byte]tokenKind{'{': tLBrace, '}': tRBrace, '[': tLBracket, ']': tRBracket, '=': tEqual, ',': tComma, ';': tSemicolon}
	if kind, ok := single[current]; ok {
		l.advanceByte()
		return token{kind: kind, text: string(current), line: line, col: col}, nil
	}
	if current == '"' {
		l.advanceByte()
		escaped := false
		for l.offset < len(l.source) {
			b := l.source[l.offset]
			l.advanceByte()
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				return token{kind: tString, text: string(l.source[start:l.offset]), line: line, col: col}, nil
			}
			if b == '\n' {
				return token{}, fmt.Errorf("transaction:%d:%d: string literal crosses a line", line, col)
			}
		}
		return token{}, fmt.Errorf("transaction:%d:%d: unterminated string literal", line, col)
	}
	if current == '-' || current >= '0' && current <= '9' {
		if current == '-' {
			l.advanceByte()
			if l.offset == len(l.source) || l.source[l.offset] < '0' || l.source[l.offset] > '9' {
				return token{}, fmt.Errorf("transaction:%d:%d: invalid integer", line, col)
			}
		}
		for l.offset < len(l.source) && l.source[l.offset] >= '0' && l.source[l.offset] <= '9' {
			l.advanceByte()
		}
		return token{kind: tInteger, text: string(l.source[start:l.offset]), line: line, col: col}, nil
	}
	if isIdentStart(current) {
		l.advanceByte()
		for l.offset < len(l.source) && isIdentContinue(l.source[l.offset]) {
			l.advanceByte()
		}
		return token{kind: tIdent, text: string(l.source[start:l.offset]), line: line, col: col}, nil
	}
	return token{}, fmt.Errorf("transaction:%d:%d: unexpected byte %q", line, col, current)
}

func (l *lexer) advanceByte() {
	if l.source[l.offset] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.offset++
}

func isIdentStart(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value == '_'
}

func isIdentContinue(value byte) bool {
	return isIdentStart(value) || value >= '0' && value <= '9' || strings.ContainsRune(".-:/", rune(value))
}
