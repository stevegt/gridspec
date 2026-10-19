// Package source discovers and parses DOT and Markdown source files.
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
)

type File struct {
	Abs    string
	Rel    string
	Kind   string
	Data   []byte
	Source model.Source
}

type Document struct {
	Path      string
	Abs       string
	Content   string
	Anchors   map[string]int
	Links     []Link
	Contracts []model.Contract
}

type Link struct {
	Destination string
	Line        int
}

type Parsed struct {
	Files       []File
	Graphs      []model.Graph
	Documents   map[string]Document
	Contracts   []model.Contract
	Diagnostics []diag.Diagnostic
}

// Discover enumerates the complete module even when CLI paths select a subset.
// Cross-file symbols therefore never change meaning under a partial invocation.
func Discover(cfg config.Config, explicit []string) ([]File, error) {
	seen := make(map[string]bool)
	var paths []string
	add := func(path string) error {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if st.IsDir() {
			return filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					if p != abs && (d.Name() == ".git" || d.Name() == "vendor") {
						return filepath.SkipDir
					}
					return nil
				}
				return addFile(cfg, p, seen, &paths)
			})
		}
		return addFile(cfg, abs, seen, &paths)
	}

	for _, root := range cfg.Roots {
		path := root
		if !filepath.IsAbs(path) {
			path = filepath.Join(cfg.Dir, path)
		}
		if err := add(path); err != nil {
			return nil, fmt.Errorf("discover root %s: %w", root, err)
		}
	}
	for _, p := range explicit {
		if !filepath.IsAbs(p) {
			p = filepath.Join(mustGetwd(), p)
		}
		if err := add(p); err != nil {
			return nil, fmt.Errorf("discover path %s: %w", p, err)
		}
	}

	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(cfg.Dir, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		kind := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		sum := sha256.Sum256(data)
		files = append(files, File{
			Abs:  path,
			Rel:  rel,
			Kind: kind,
			Data: data,
			Source: model.Source{
				Path: rel, Kind: kind, SHA256: hex.EncodeToString(sum[:]),
			},
		})
	}
	return files, nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func addFile(cfg config.Config, abs string, seen map[string]bool, paths *[]string) error {
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".dot" && ext != ".md" && ext != ".markdown" {
		return nil
	}
	rel, err := filepath.Rel(cfg.Dir, abs)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		// Never let an explicit path silently expand the module authority boundary.
		return nil
	}
	if !matchesAny(rel, cfg.Include) || matchesAny(rel, cfg.Exclude) {
		return nil
	}
	if !seen[abs] {
		seen[abs] = true
		*paths = append(*paths, abs)
	}
	return nil
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if glob(pattern, path) {
			return true
		}
	}
	return false
}

// glob implements the small, portable subset needed by manifests: *, ?, and
// ** spanning path separators.
func glob(pattern, value string) bool {
	pattern, value = filepath.ToSlash(pattern), filepath.ToSlash(value)
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteByte('$')
	ok, err := regexp.MatchString(b.String(), value)
	return err == nil && ok
}

// Parse compiles all discovered sources into syntax-level objects.
func Parse(files []File) Parsed {
	result := Parsed{Files: files, Documents: make(map[string]Document)}
	for _, f := range files {
		switch f.Kind {
		case "dot":
			graphs, ds := parseDOT(f)
			result.Graphs = append(result.Graphs, graphs...)
			result.Diagnostics = append(result.Diagnostics, ds...)
		case "md", "markdown":
			doc, ds := parseMarkdown(f)
			result.Documents[f.Rel] = doc
			result.Contracts = append(result.Contracts, doc.Contracts...)
			result.Diagnostics = append(result.Diagnostics, ds...)
		}
	}
	return result
}
