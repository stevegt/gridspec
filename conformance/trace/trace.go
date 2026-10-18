// Package trace emits deterministic semantic execution evidence for
// specdot conform. Implementations may use this package or emit the documented
// NDJSON protocol themselves.
package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

const (
	Schema      = "specdot.trace/v1"
	FileEnv     = "SPECDOT_TRACE_FILE"
	ScenarioEnv = "SPECDOT_TRACE_SCENARIO"
)

type Event struct {
	Schema     string            `json:"schema"`
	ScenarioID string            `json:"scenario_id"`
	SpecID     string            `json:"spec_id"`
	Phase      string            `json:"phase,omitempty"`
	Outcome    string            `json:"outcome,omitempty"`
	Sequence   int               `json:"sequence"`
	Fields     map[string]string `json:"fields,omitempty"`
}

type Sink interface {
	Emit(specID, phase, outcome string, fields map[string]string) error
}

type Encoder struct {
	mu       sync.Mutex
	writer   io.Writer
	scenario string
	sequence int
}

func NewEncoder(w io.Writer, scenario string) *Encoder {
	return &Encoder{writer: w, scenario: scenario}
}

func (e *Encoder) Emit(specID, phase, outcome string, fields map[string]string) error {
	if e == nil || e.writer == nil {
		return nil
	}
	if specID == "" {
		return errors.New("trace event spec ID is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sequence++
	return json.NewEncoder(e.writer).Encode(Event{
		Schema: Schema, ScenarioID: e.scenario, SpecID: specID,
		Phase: phase, Outcome: outcome, Sequence: e.sequence, Fields: fields,
	})
}

// OpenFromEnv opens the trace requested by a conformance command runner. A
// missing environment variable returns a nil sink and a no-op closer.
func OpenFromEnv() (Sink, func() error, error) {
	path := os.Getenv(FileEnv)
	if path == "" {
		return nil, func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}
	w := bufio.NewWriter(f)
	closeFn := func() error {
		if err := w.Flush(); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
	return NewEncoder(w, os.Getenv(ScenarioEnv)), closeFn, nil
}
