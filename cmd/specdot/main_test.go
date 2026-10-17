package main

import (
	"reflect"
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
