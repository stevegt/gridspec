package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVetSelfSpecificationBuildReady(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{
		ConfigPath: filepath.Join(root, "specdot.yaml"),
		Profile:    "build", Format: "json", Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Vet() code = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result struct {
		Module  string `json:"module"`
		Summary struct {
			Errors   int `json:"errors"`
			Warnings int `json:"warnings"`
			Notes    int `json:"notes"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode self-spec report: %v\n%s", err, stdout.String())
	}
	if result.Module != "github.com/promisegrid/gridspec" {
		t.Fatalf("module = %q", result.Module)
	}
	if result.Summary.Errors != 0 || result.Summary.Warnings != 0 || result.Summary.Notes != 0 {
		t.Fatalf("self-spec report is not clean: %+v", result.Summary)
	}
}

func TestVetHealthyModule(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{
		ConfigPath: filepath.Join(root, "examples", "healthy", "specdot.yaml"),
		Profile:    "default", Format: "json", Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("Vet() code = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"errors": 0`) {
		t.Fatalf("healthy report does not say zero errors: %s", stdout.String())
	}
}

func TestVetCatchesCarMisfireClass(t *testing.T) {
	manifest := filepath.Clean(filepath.Join("..", "..", "testdata", "car-misfire", "specdot.yaml"))
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{ConfigPath: manifest, Profile: "default", Format: "text", Stdout: &stdout, Stderr: &stderr})
	if code != 1 {
		t.Fatalf("Vet() code = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, rule := range []string{"SDV411", "SDV520", "SDV521"} {
		if !strings.Contains(stdout.String(), rule) {
			t.Errorf("report is missing %s:\n%s", rule, stdout.String())
		}
	}
}

func TestEmittedIRIsDeterministic(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{filepath.Join(t.TempDir(), "one.json"), filepath.Join(t.TempDir(), "two.json")}
	for _, path := range paths {
		var stdout, stderr bytes.Buffer
		if code := Vet(VetOptions{ConfigPath: filepath.Join(root, "specdot.yaml"), Profile: "build", EmitIR: path, Stdout: &stdout, Stderr: &stderr}); code != 0 {
			t.Fatalf("Vet() code = %d: %s %s", code, stdout.String(), stderr.String())
		}
	}
	one, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("canonical IR differs across identical runs")
	}
}

func TestVetLegacyConfigFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"), Stdout: &stdout, Stderr: &stderr})
	if code != 2 || !strings.Contains(stderr.String(), "specdot vet:") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestVetLegacyEmitFailure(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{
		ConfigPath: filepath.Join(root, "examples", "healthy", "specdot.yaml"),
		Profile:    "default", Format: "text", EmitIR: root,
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 2 || !strings.Contains(stderr.String(), "emit IR") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
