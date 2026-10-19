package source

import (
	"bytes"
	"strings"

	"github.com/promisegrid/gridspec/internal/model"
)

type lineIndex struct {
	path   string
	data   []byte
	starts []int
}

func newLineIndex(path string, data []byte) lineIndex {
	starts := []int{0}
	for i, b := range data {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lineIndex{path: path, data: data, starts: starts}
}

func (l lineIndex) atOffset(offset int) model.Location {
	if offset < 0 {
		offset = 0
	}
	line := 0
	for i := 1; i < len(l.starts); i++ {
		if l.starts[i] > offset {
			break
		}
		line = i
	}
	return model.Location{File: l.path, Line: line + 1, Column: offset - l.starts[line] + 1}
}

func (l lineIndex) find(values ...string) model.Location {
	for _, value := range values {
		if value == "" {
			continue
		}
		if at := bytes.Index(l.data, []byte(value)); at >= 0 {
			return l.atOffset(at)
		}
	}
	return model.Location{File: l.path, Line: 1, Column: 1}
}

func modelToDiagLocation(l model.Location) (file string, line, column int) {
	return l.File, l.Line, l.Column
}

func unquoteDOT(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var b strings.Builder
		for i := 1; i < len(s)-1; i++ {
			if s[i] == '\\' && i+1 < len(s)-1 {
				i++
				switch s[i] {
				case 'n', 'l', 'r':
					b.WriteByte('\n')
				default:
					b.WriteByte(s[i])
				}
				continue
			}
			b.WriteByte(s[i])
		}
		return b.String()
	}
	return s
}
