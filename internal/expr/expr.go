// Package expr validates and evaluates specdot's restricted CEL subset.
package expr

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	"github.com/promisegrid/specdot/internal/model"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Environment is deterministic and has no host I/O, clock, random source, or
// mutation hooks. Only declared machine variables plus state/event are visible.
type Environment struct {
	env       *cel.Env
	variables map[string]model.Variable
}

func New(variables []model.Variable) (*Environment, error) {
	decls := []cel.EnvOption{
		cel.Variable("state", cel.StringType),
		cel.Variable("event", cel.StringType),
		cel.Variable("data", cel.DynType),
		cel.Variable("subject", cel.DynType),
		cel.Variable("context", cel.DynType),
	}
	vars := make(map[string]model.Variable)
	for _, variable := range variables {
		if !identifier.MatchString(variable.Name) {
			return nil, fmt.Errorf("invalid CEL variable name %q", variable.Name)
		}
		if _, exists := vars[variable.Name]; exists {
			return nil, fmt.Errorf("duplicate CEL variable %q", variable.Name)
		}
		vars[variable.Name] = variable
		decls = append(decls, cel.Variable(variable.Name, celType(variable.Type)))
	}
	env, err := cel.NewEnv(decls...)
	if err != nil {
		return nil, err
	}
	return &Environment{env: env, variables: vars}, nil
}

func celType(name string) *cel.Type {
	switch name {
	case "bool":
		return cel.BoolType
	case "int", "int64":
		return cel.IntType
	case "uint", "uint64":
		return cel.UintType
	case "double", "float":
		return cel.DoubleType
	case "string":
		return cel.StringType
	case "bytes":
		return cel.BytesType
	default:
		return cel.DynType
	}
}

// CompileBool type-checks an expression and requires a boolean result.
func (e *Environment) CompileBool(source string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	ast, issues := e.env.Compile(source)
	if issues != nil && issues.Err() != nil {
		return issues.Err()
	}
	if !cel.BoolType.IsAssignableType(ast.OutputType()) && !ast.OutputType().IsEquivalentType(cel.DynType) {
		return fmt.Errorf("expression returns %s, want bool", ast.OutputType())
	}
	return nil
}

// EvalBool evaluates a previously valid boolean expression.
func (e *Environment) EvalBool(source, state, event string, variables map[string]any) (bool, error) {
	if strings.TrimSpace(source) == "" {
		return true, nil
	}
	ast, issues := e.env.Compile(source)
	if issues != nil && issues.Err() != nil {
		return false, issues.Err()
	}
	program, err := e.env.Program(ast)
	if err != nil {
		return false, err
	}
	activation := make(map[string]any, len(variables)+5)
	activation["state"], activation["event"] = state, event
	activation["data"], activation["subject"], activation["context"] = map[string]any{}, map[string]any{}, map[string]any{}
	for name, value := range variables {
		activation[name] = value
	}
	value, _, err := program.Eval(activation)
	if err != nil {
		return false, err
	}
	result, ok := value.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression returned %T, want bool", value.Value())
	}
	return result, nil
}

// CompileAction validates semicolon-separated assignments and returns them in
// source order. Each right-hand side is CEL and can reference current values.
func (e *Environment) CompileAction(source string) ([]Assignment, error) {
	var result []Assignment
	for _, statement := range strings.Split(source, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		parts := strings.SplitN(statement, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("action %q is not an assignment", statement)
		}
		name, rhs := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if _, ok := e.variables[name]; !ok {
			return nil, fmt.Errorf("action assigns undeclared variable %q", name)
		}
		ast, issues := e.env.Compile(rhs)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("%s: %w", name, issues.Err())
		}
		expected := celType(e.variables[name].Type)
		if !expected.IsEquivalentType(cel.DynType) && !expected.IsAssignableType(ast.OutputType()) && !ast.OutputType().IsEquivalentType(cel.DynType) {
			return nil, fmt.Errorf("action assigns %s to %s variable %q", ast.OutputType(), expected, name)
		}
		program, err := e.env.Program(ast)
		if err != nil {
			return nil, err
		}
		result = append(result, Assignment{Name: name, Source: rhs, program: program})
	}
	return result, nil
}

type Assignment struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	program cel.Program
}

func (a Assignment) Eval(state, event string, variables map[string]any) (any, error) {
	activation := make(map[string]any, len(variables)+5)
	activation["state"], activation["event"] = state, event
	activation["data"], activation["subject"], activation["context"] = map[string]any{}, map[string]any{}, map[string]any{}
	for name, value := range variables {
		activation[name] = value
	}
	value, _, err := a.program.Eval(activation)
	if err != nil {
		return nil, err
	}
	return value.Value(), nil
}

// Names returns the declared variable names in deterministic order.
func (e *Environment) Names() []string {
	names := make([]string, 0, len(e.variables))
	for name := range e.variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
