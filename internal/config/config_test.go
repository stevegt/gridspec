package config

import "testing"

func TestSelectedProfileRejectsInvalidBounds(t *testing.T) {
	cfg := defaults()
	cfg.Profiles["bad"] = Profile{Finite: FiniteProfile{Enabled: true, MaxStates: -1, MaxDepth: 1, MaxQueue: 1, MaxTransitions: 1}}
	if _, err := cfg.SelectedProfile("bad"); err == nil {
		t.Fatal("expected invalid finite bounds to fail")
	}
}

func TestSelectedProfileRejectsInvalidSeverity(t *testing.T) {
	cfg := defaults()
	cfg.Profiles["bad"] = Profile{Rules: map[string]string{"SDV119": "maybe"}}
	if _, err := cfg.SelectedProfile("bad"); err == nil {
		t.Fatal("expected invalid severity to fail")
	}
}
