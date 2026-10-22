package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpUsesCleanBreakName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "usage: gridspec") || strings.Contains(stdout.String(), "usage: specdot") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func TestVetRejectsUnsupportedFormatBeforeRepositoryAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"vet", "--format", "xml"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid --format") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestViewRejectsInvalidLabelFormatBeforeRepositoryAccess(t *testing.T) {
	for _, args := range [][]string{
		{"view", "--format", "dot", "--label-format", "{title"},
		{"view", "--format", "text", "--label-format", "{title}"},
		{"view", "--format", "invalid", "--label-format", "{title}"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "label-format") && !strings.Contains(stderr.String(), "label format") {
			t.Fatalf("%v stderr = %q", args, stderr.String())
		}
	}
}

func TestDiffRejectsUsageBeforeRepositoryAccess(t *testing.T) {
	for _, args := range [][]string{
		{"diff"},
		{"diff", "--proposal", "proposal", "--format", "yaml"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "gridspec diff") {
			t.Fatalf("%v stderr=%q", args, stderr.String())
		}
	}
}

func TestProposalUpdateCannotWrite(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"update", "--proposal", "proposal", "--write"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--write requires accepted specifications") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestProposalConformanceCannotPublishAuthorityArtifacts(t *testing.T) {
	for _, option := range []string{"--record", "--emit-baseline=baseline.json"} {
		var stdout, stderr bytes.Buffer
		args := []string{"conform", "--proposal", "proposal", option}
		if option == "--record" {
			args = append(args, "--format", "json")
		}
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%s code=%d stderr=%s", option, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "cannot record evidence or emit a baseline") {
			t.Fatalf("%s stderr = %q", option, stderr.String())
		}
	}
}
