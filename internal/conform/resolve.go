package conform

import (
	"fmt"
	"go/types"
	"sort"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
	"golang.org/x/tools/go/packages"
)

func resolveBindings(cfg Config, ir model.IR, locations map[string]model.Location) (map[string]bool, []diag.Diagnostic) {
	evidence := make(map[string]bool)
	if len(cfg.Bindings) == 0 {
		return evidence, nil
	}
	patternsSet := make(map[string]bool)
	for _, binding := range cfg.Bindings {
		patternsSet[binding.Package] = true
	}
	patterns := make([]string, 0, len(patternsSet))
	for pattern := range patternsSet {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	packageConfig := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedTypesInfo,
		Dir: cfg.ModuleRoot,
	}
	loaded, err := packages.Load(packageConfig, patterns...)
	if err != nil {
		return evidence, []diag.Diagnostic{conformDiagnostic("SDC100", model.Location{File: cfg.Path}, "load Go packages: "+err.Error(), "", "fix go.module_root and binding package paths")}
	}
	byPath := make(map[string]*packages.Package)
	for _, pkg := range loaded {
		byPath[pkg.PkgPath] = pkg
		byPath[pkg.ID] = pkg
	}
	var diagnostics []diag.Diagnostic
	packageReported := make(map[string]bool)
	for _, binding := range cfg.Bindings {
		loc := locations[binding.SpecID]
		if loc.File == "" {
			loc = model.Location{File: cfg.Path}
		}
		if !hasSpecID(ir, binding.SpecID) {
			continue // The orchestrator emits the common stale-ID diagnostic.
		}
		pkg := byPath[binding.Package]
		if pkg == nil {
			matches, loadErr := packages.Load(packageConfig, binding.Package)
			if loadErr == nil && len(matches) == 1 {
				pkg = matches[0]
				byPath[binding.Package] = pkg
			}
			if pkg == nil {
				message := fmt.Sprintf("binding for %s cannot load one Go package from %q", binding.SpecID, binding.Package)
				if loadErr != nil {
					message += ": " + loadErr.Error()
				}
				diagnostics = append(diagnostics, conformDiagnostic("SDC101", loc, message, binding.SpecID, "use an import path or single-package pattern resolvable from go.module_root"))
				continue
			}
		}
		if len(pkg.Errors) != 0 {
			if !packageReported[binding.Package] {
				packageReported[binding.Package] = true
				for _, packageErr := range pkg.Errors {
					diagnostics = append(diagnostics, conformDiagnostic("SDC101", loc, fmt.Sprintf("binding for %s cannot type-check %s: %s", binding.SpecID, binding.Package, packageErr.Msg), binding.SpecID, "repair the Go package before checking conformance"))
				}
			}
			continue
		}
		if err := resolveObject(pkg.Types, binding); err != nil {
			diagnostics = append(diagnostics, conformDiagnostic("SDC102", loc, fmt.Sprintf("binding for %s does not resolve: %v", binding.SpecID, err), binding.SpecID, "update the implementation or the reviewed sidecar binding"))
			continue
		}
		evidence[binding.SpecID] = true
	}
	return evidence, diagnostics
}

func resolveObject(pkg *types.Package, binding Binding) error {
	if pkg == nil {
		return fmt.Errorf("package has no type information")
	}
	if binding.Kind == "package" {
		return nil
	}
	if binding.Receiver == "" {
		obj := pkg.Scope().Lookup(binding.Name)
		if obj == nil {
			return fmt.Errorf("%s %q is absent from package %s", binding.Kind, binding.Name, pkg.Path())
		}
		if !objectKind(obj, binding.Kind) {
			return fmt.Errorf("%q is %T, not %s", binding.Name, obj, binding.Kind)
		}
		return nil
	}
	receiverObj := pkg.Scope().Lookup(binding.Receiver)
	typeName, ok := receiverObj.(*types.TypeName)
	if !ok {
		return fmt.Errorf("receiver type %q is absent", binding.Receiver)
	}
	named, ok := typeName.Type().(*types.Named)
	if !ok {
		return fmt.Errorf("receiver %q is not a named type", binding.Receiver)
	}
	if binding.Kind == "method" {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, pkg, binding.Name)
		fn, ok := obj.(*types.Func)
		if !ok || fn == nil {
			return fmt.Errorf("method %s.%s is absent", binding.Receiver, binding.Name)
		}
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, pkg, binding.Name)
	field, ok := obj.(*types.Var)
	if !ok || !field.IsField() {
		return fmt.Errorf("field %s.%s is absent", binding.Receiver, binding.Name)
	}
	return nil
}

func objectKind(obj types.Object, kind string) bool {
	switch kind {
	case "func":
		fn, ok := obj.(*types.Func)
		return ok && fn.Type().(*types.Signature).Recv() == nil
	case "type":
		_, ok := obj.(*types.TypeName)
		return ok
	case "const":
		_, ok := obj.(*types.Const)
		return ok
	case "var":
		_, ok := obj.(*types.Var)
		return ok
	default:
		return false
	}
}
