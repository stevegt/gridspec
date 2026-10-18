package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestReorderVetArgsAllowsInterspersedFlags(t *testing.T) {
	ordered, paths, err := reorderVetArgs([]string{"specs", "--profile", "build", "other.dot", "--online", "--format=json"})
	if err != nil {
		t.Fatal(err)
	}
	wantOrdered := []string{"--profile", "build", "--online", "--format=json"}
	wantPaths := []string{"specs", "other.dot"}
	if !reflect.DeepEqual(ordered, wantOrdered) || !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("ordered=%v paths=%v", ordered, paths)
	}
}

func TestConformRejectsUnsupportedFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"conform", "--format", "sarif"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid --format") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
