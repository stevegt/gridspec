// Package model defines specdot's canonical, generator-facing IR.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

const IRSchema = "specdot.ir/v1"

type Source struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// IR is the stable handoff consumed by vet today and specdot build later.
type IR struct {
	Schema      string     `json:"schema"`
	Module      string     `json:"module"`
	Profile     string     `json:"profile"`
	Sources     []Source   `json:"sources"`
	Graphs      []Graph    `json:"graphs"`
	Contracts   []Contract `json:"contracts"`
	Bindings    []Binding  `json:"bindings,omitempty"`
	CheckResult []CheckRun `json:"checks,omitempty"`
	Digest      string     `json:"digest,omitempty"`
	RootDir     string     `json:"-"`
}

type Graph struct {
	ID       string            `json:"id"`
	DotID    string            `json:"dot_id,omitempty"`
	Kind     string            `json:"kind"`
	Status   string            `json:"status"`
	Directed bool              `json:"directed"`
	Strict   bool              `json:"strict"`
	URL      string            `json:"url,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Nodes    []Element         `json:"nodes"`
	Edges    []Element         `json:"edges"`
	Location Location          `json:"location"`
}

// Element represents either a declared DOT node or edge. For edges From and To
// contain semantic node IDs, never incidental DOT identifiers.
type Element struct {
	ID       string            `json:"id"`
	DotID    string            `json:"dot_id,omitempty"`
	GraphID  string            `json:"graph_id"`
	Element  string            `json:"element"` // node or edge
	Kind     string            `json:"kind"`
	Status   string            `json:"status"`
	From     string            `json:"from,omitempty"`
	To       string            `json:"to,omitempty"`
	URL      string            `json:"url,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Location Location          `json:"location"`
}

type Contract struct {
	Schema     string              `json:"schema" yaml:"schema"`
	For        string              `json:"for" yaml:"for"`
	Kind       string              `json:"kind,omitempty" yaml:"kind,omitempty"`
	Status     string              `json:"status,omitempty" yaml:"status,omitempty"`
	Summary    string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Agent      *AgentContract      `json:"agent,omitempty" yaml:"agent,omitempty"`
	Promise    *PromiseContract    `json:"promise,omitempty" yaml:"promise,omitempty"`
	Signal     *SignalContract     `json:"signal,omitempty" yaml:"signal,omitempty"`
	Assessment *AssessmentContract `json:"assessment,omitempty" yaml:"assessment,omitempty"`
	Machine    *MachineContract    `json:"machine,omitempty" yaml:"machine,omitempty"`
	Data       *DataContract       `json:"data,omitempty" yaml:"data,omitempty"`
	Channel    *ChannelContract    `json:"channel,omitempty" yaml:"channel,omitempty"`
	Effect     *EffectContract     `json:"effect,omitempty" yaml:"effect,omitempty"`
	Claims     []Claim             `json:"claims,omitempty" yaml:"claims,omitempty"`
	Waivers    []Waiver            `json:"waivers,omitempty" yaml:"waivers,omitempty"`
	Latitude   []Latitude          `json:"latitude,omitempty" yaml:"latitude,omitempty"`
	Metadata   map[string]string   `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Location   Location            `json:"location" yaml:"-"`
}

type AgentContract struct {
	Controls     []string `json:"controls,omitempty" yaml:"controls,omitempty"`
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Machines     []string `json:"machines,omitempty" yaml:"machines,omitempty"`
	Members      []string `json:"members,omitempty" yaml:"members,omitempty"`
}

// SignalContract covers informational signals and impositions. Neither is a
// promise: it records an attempted influence, not autonomous acceptance.
type SignalContract struct {
	Sender    string `json:"sender" yaml:"sender"`
	Recipient string `json:"recipient" yaml:"recipient"`
	Mode      string `json:"mode" yaml:"mode"` // signal or imposition
	Body      Body   `json:"body" yaml:"body"`
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"`
}

// AssessmentContract is explicitly observer-local and never grants authority.
type AssessmentContract struct {
	Observer  string `json:"observer" yaml:"observer"`
	Subject   string `json:"subject" yaml:"subject"`
	Predicate string `json:"predicate" yaml:"predicate"`
	Evidence  string `json:"evidence" yaml:"evidence"`
}

type PromiseContract struct {
	Promiser     string   `json:"promiser" yaml:"promiser"`
	Promisee     string   `json:"promisee" yaml:"promisee"`
	Scope        []string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Polarity     string   `json:"polarity" yaml:"polarity"`
	Body         Body     `json:"body" yaml:"body"`
	Prerequisite string   `json:"prerequisite,omitempty" yaml:"prerequisite,omitempty"`
	Condition    string   `json:"condition,omitempty" yaml:"condition,omitempty"`
	Evidence     string   `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	External     bool     `json:"external,omitempty" yaml:"external,omitempty"`
}

type Body struct {
	Type        string `json:"type" yaml:"type"`
	Constraints string `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Resource    string `json:"resource,omitempty" yaml:"resource,omitempty"`
}

type Variable struct {
	Name    string `json:"name" yaml:"name"`
	Type    string `json:"type" yaml:"type"`
	Initial any    `json:"initial" yaml:"initial"`
	Min     *int64 `json:"min,omitempty" yaml:"min,omitempty"`
	Max     *int64 `json:"max,omitempty" yaml:"max,omitempty"`
	Values  []any  `json:"values,omitempty" yaml:"values,omitempty"`
}

type EventContract struct {
	Name    string `json:"name" yaml:"name"`
	Payload string `json:"payload,omitempty" yaml:"payload,omitempty"`
	Policy  string `json:"policy" yaml:"policy"` // handled, deferred, refused, ignored
}

type MachineContract struct {
	Agent     string          `json:"agent" yaml:"agent"`
	Variables []Variable      `json:"variables,omitempty" yaml:"variables,omitempty"`
	Events    []EventContract `json:"events,omitempty" yaml:"events,omitempty"`
	Semantics string          `json:"semantics,omitempty" yaml:"semantics,omitempty"`
}

type Field struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"`
	Optional bool   `json:"optional,omitempty" yaml:"optional,omitempty"`
	Min      *int   `json:"min,omitempty" yaml:"min,omitempty"`
	Max      *int   `json:"max,omitempty" yaml:"max,omitempty"`
}

type DataContract struct {
	Type           string   `json:"type" yaml:"type"`
	Base           string   `json:"base,omitempty" yaml:"base,omitempty"`
	KeyType        string   `json:"key_type,omitempty" yaml:"key_type,omitempty"`
	ValueType      string   `json:"value_type,omitempty" yaml:"value_type,omitempty"`
	Fields         []Field  `json:"fields,omitempty" yaml:"fields,omitempty"`
	Values         []string `json:"values,omitempty" yaml:"values,omitempty"`
	Variants       []string `json:"variants,omitempty" yaml:"variants,omitempty"`
	Identity       []string `json:"identity,omitempty" yaml:"identity,omitempty"`
	Authority      string   `json:"authority,omitempty" yaml:"authority,omitempty"`
	Mutable        *bool    `json:"mutable,omitempty" yaml:"mutable,omitempty"`
	Lifecycle      []string `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	Encoding       string   `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	Version        string   `json:"version,omitempty" yaml:"version,omitempty"`
	Migration      string   `json:"migration,omitempty" yaml:"migration,omitempty"`
	Secret         bool     `json:"secret,omitempty" yaml:"secret,omitempty"`
	Exclusive      bool     `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
	Retention      []string `json:"retention,omitempty" yaml:"retention,omitempty"`
	PhysicalIDType string   `json:"physical_id_type,omitempty" yaml:"physical_id_type,omitempty"`
}

type ChannelContract struct {
	From       string `json:"from" yaml:"from"`
	To         string `json:"to" yaml:"to"`
	Payload    string `json:"payload" yaml:"payload"`
	Delivery   string `json:"delivery" yaml:"delivery"`
	Ordering   string `json:"ordering" yaml:"ordering"`
	Duplicate  string `json:"duplicate" yaml:"duplicate"`
	Loss       string `json:"loss" yaml:"loss"`
	Capacity   int    `json:"capacity" yaml:"capacity"`
	Durability string `json:"durability" yaml:"durability"`
	Integrity  string `json:"integrity" yaml:"integrity"`
	Timing     string `json:"timing" yaml:"timing"`
	Partition  string `json:"partition" yaml:"partition"`
	Restart    string `json:"restart" yaml:"restart"`
	External   bool   `json:"external,omitempty" yaml:"external,omitempty"`
}

type EffectContract struct {
	Agent       string   `json:"agent" yaml:"agent"`
	Operation   string   `json:"operation" yaml:"operation"`
	Resource    string   `json:"resource" yaml:"resource"`
	Outcomes    []string `json:"outcomes" yaml:"outcomes"`
	RetainOn    []string `json:"retain_on,omitempty" yaml:"retain_on,omitempty"`
	DeleteOn    []string `json:"delete_on,omitempty" yaml:"delete_on,omitempty"`
	Idempotency string   `json:"idempotency,omitempty" yaml:"idempotency,omitempty"`
	Correlation string   `json:"correlation,omitempty" yaml:"correlation,omitempty"`
	Retry       string   `json:"retry,omitempty" yaml:"retry,omitempty"`
	Reconcile   string   `json:"reconcile,omitempty" yaml:"reconcile,omitempty"`
	Evidence    string   `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Assessor    string   `json:"assessor,omitempty" yaml:"assessor,omitempty"`
}

type Claim struct {
	ID          string   `json:"id" yaml:"id"`
	Kind        string   `json:"kind" yaml:"kind"`
	Expression  string   `json:"expression,omitempty" yaml:"expression,omitempty"`
	State       string   `json:"state,omitempty" yaml:"state,omitempty"`
	From        string   `json:"from,omitempty" yaml:"from,omitempty"`
	To          string   `json:"to,omitempty" yaml:"to,omitempty"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Assumptions []string `json:"assumptions,omitempty" yaml:"assumptions,omitempty"`
}

type Waiver struct {
	Rule       string `json:"rule" yaml:"rule"`
	Reason     string `json:"reason" yaml:"reason"`
	Owner      string `json:"owner" yaml:"owner"`
	Expires    string `json:"expires" yaml:"expires"`
	Risk       string `json:"risk" yaml:"risk"`
	Mitigation string `json:"mitigation" yaml:"mitigation"`
}

type Latitude struct {
	Area       string   `json:"area" yaml:"area"`
	Allowed    []string `json:"allowed" yaml:"allowed"`
	Constraint string   `json:"constraint,omitempty" yaml:"constraint,omitempty"`
	Owner      string   `json:"owner,omitempty" yaml:"owner,omitempty"`
}

type Binding struct {
	Offer      string `json:"offer"`
	Acceptance string `json:"acceptance"`
	BodyType   string `json:"body_type"`
}

type CheckRun struct {
	Machine     string      `json:"machine"`
	Profile     string      `json:"profile"`
	Digest      string      `json:"digest"`
	States      int         `json:"states"`
	Steps       int         `json:"steps"`
	Status      string      `json:"status"` // passed, violated, inconclusive, assumed, unchecked
	Claims      []ClaimRun  `json:"claims,omitempty"`
	Trace       []TraceStep `json:"trace,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Assumptions []string    `json:"assumptions,omitempty"`
}

type ClaimRun struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type TraceStep struct {
	Depth      int            `json:"depth"`
	State      string         `json:"state"`
	Event      string         `json:"event,omitempty"`
	Transition string         `json:"transition,omitempty"`
	Variables  map[string]any `json:"variables,omitempty"`
}

// Normalize sorts order-insensitive collections before encoding.
func (ir *IR) Normalize() {
	sort.Slice(ir.Sources, func(i, j int) bool { return ir.Sources[i].Path < ir.Sources[j].Path })
	sort.Slice(ir.Graphs, func(i, j int) bool { return ir.Graphs[i].ID < ir.Graphs[j].ID })
	for i := range ir.Graphs {
		sort.Slice(ir.Graphs[i].Nodes, func(a, b int) bool { return ir.Graphs[i].Nodes[a].ID < ir.Graphs[i].Nodes[b].ID })
		sort.Slice(ir.Graphs[i].Edges, func(a, b int) bool { return ir.Graphs[i].Edges[a].ID < ir.Graphs[i].Edges[b].ID })
	}
	sort.Slice(ir.Contracts, func(i, j int) bool { return ir.Contracts[i].For < ir.Contracts[j].For })
	sort.Slice(ir.Bindings, func(i, j int) bool {
		if ir.Bindings[i].Offer != ir.Bindings[j].Offer {
			return ir.Bindings[i].Offer < ir.Bindings[j].Offer
		}
		return ir.Bindings[i].Acceptance < ir.Bindings[j].Acceptance
	})
}

// Seal computes the content digest. Digest is omitted from its own preimage.
func (ir *IR) Seal() error {
	ir.Normalize()
	ir.Digest = ""
	b, err := json.Marshal(ir)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	ir.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return nil
}
