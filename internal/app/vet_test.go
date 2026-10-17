package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVetHealthyModule(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	var stdout, stderr bytes.Buffer
	code := Vet(VetOptions{
		ConfigPath: filepath.Join(root, "specdot.yaml"),
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
		if code := Vet(VetOptions{ConfigPath: filepath.Join(root, "specdot.yaml"), Profile: "default", EmitIR: path, Stdout: &stdout, Stderr: &stderr}); code != 0 {
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
