package trace

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncoderProducesDeterministicSequence(t *testing.T) {
	var out bytes.Buffer
	sink := NewEncoder(&out, "scenario.example")
	if err := sink.Emit("step.one", "enter", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit("step.two", "exit", "success", map[string]string{"key": "value"}); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	for want := 1; want <= 2; want++ {
		var event Event
		if err := dec.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Schema != Schema || event.ScenarioID != "scenario.example" || event.Sequence != want {
			t.Fatalf("event = %#v", event)
		}
	}
}
