package layout

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const internalPrefix = "github.com/weaveplatform/spaceweave/internal/"

var modules = map[string]bool{
	"floorplans":    true,
	"identity":      true,
	"workplace":     true,
	"policy":        true,
	"booking":       true,
	"exchange":      true,
	"visitors":      true,
	"events":        true,
	"attendance":    true,
	"notifications": true,
}

func knownRoot(root string) bool {
	return modules[root] || root == "app" || root == "platform" || root == "gateway" || root == "kernel" ||
		root == "layout" || root == "transport" ||
		root == "devtools" ||
		root == "ctl"
}

func rootOf(path string) string {
	root, _, _ := strings.Cut(path, "/")
	return root
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// importViolation checks ownership independently of which modules happen to
// exist today. Tests may use concrete fixtures; production callers cross only
// the published module interface, with construction reserved for app.
func importViolation(source, imported string) string {
	owner := rootOf(source)
	if !knownRoot(owner) {
		return "unknown internal owner: " + owner
	}
	dependency, internal := strings.CutPrefix(imported, internalPrefix)
	if internal && !knownRoot(rootOf(dependency)) {
		return "unknown internal dependency: " + dependency
	}
	if owner == "kernel" && (internal || strings.Contains(rootOf(imported), ".")) {
		return "kernel must use only the standard library"
	}
	if modules[owner] && within(source, owner+"/api") {
		if (internal && !within(dependency, "kernel")) ||
			(!internal && strings.Contains(rootOf(imported), ".")) {
			return "module api may import only the standard library and kernel"
		}
	}
	if modules[owner] && within(source, owner+"/core") {
		if internal && !within(dependency, owner+"/api") && !within(dependency, owner+"/core") &&
			!within(dependency, "kernel") {
			return "module core may import only its own core, api and kernel"
		}
		if !internal && strings.Contains(rootOf(imported), ".") {
			return "module core must not import external dependencies"
		}
	}
	if !internal {
		return ""
	}
	target := rootOf(dependency)
	switch {
	case owner == "ctl" && !within(dependency, "platform/buildinfo"):
		return "remote CLI must use public clients instead of internal server code"
	case owner == "transport" && target != "transport":
		return "generated transport must not import application implementation"
	case owner == "devtools" && target != "devtools":
		return "developer tooling must not import runtime modules"
	case owner == "app":
		return ""
	case owner == "platform" && target != "platform" && target != "kernel":
		return "platform must not import application owners"
	case owner == "gateway":
		if target == "app" || (modules[target] && !within(dependency, target+"/api")) {
			return "gateway must use published module interfaces"
		}
	case modules[owner]:
		if target == "app" || target == "gateway" {
			return "module implementation must not import application assembly or gateways"
		}
		if target != owner && modules[target] && !within(dependency, target+"/api") {
			return "cross-module imports must use the target api"
		}
		if within(source, owner+"/processing") &&
			(within(dependency, owner+"/adapters") || target == "transport") {
			return "processing must not import adapters"
		}
	}
	return ""
}

func productionViolation(source, imported string) string {
	if owned, internal := strings.CutPrefix(source, "internal/"); internal {
		return importViolation(owned, imported)
	}
	dependency, internal := strings.CutPrefix(imported, internalPrefix)
	if !internal {
		return ""
	}
	if !knownRoot(rootOf(dependency)) {
		return "unknown internal dependency: " + dependency
	}
	if within(source, "pkg") {
		return "published packages must not import internal implementation"
	}
	if within(source, "tools") && !within(dependency, "devtools") {
		return "generation commands must delegate to developer tooling"
	}
	if within(source, "internal/ctl") && !(within(dependency, "platform/buildinfo") || !internal) {
		return "remote CLI must use the public SDK"
	}
	if within(source, "cmd/spaceweavectl") {
		if !within(dependency, "ctl") {
			return "control entrypoint must delegate to remote CLI"
		}
		return ""
	}
	if within(source, "cmd") && !within(dependency, "app") {
		return "entrypoints must delegate to application assembly"
	}
	return ""
}

func TestProductionImportsRespectModuleOwnership(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk source: %w", walkErr)
		}
		if entry.IsDir() {
			if path != root &&
				(strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" ||
					entry.Name() == "bin" || entry.Name() == "cover" || entry.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("identify package: %w", err)
		}
		source := filepath.ToSlash(relative)
		if owned, internal := strings.CutPrefix(
			source,
			"internal/",
		); internal &&
			!knownRoot(rootOf(owned)) {
			t.Errorf("%s: unknown internal owner", owned)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports: %w", err)
		}
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("read import: %w", err)
			}
			if violation := productionViolation(source, imported); violation != "" {
				t.Errorf("%s imports %s: %s", source, imported, violation)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEntrypointAndPublicImports(t *testing.T) {
	cases := []struct {
		source, imported string
		allowed          bool
	}{
		{"cmd/spaceweave", internalPrefix + "app/cli", true},
		{"cmd/spaceweave", internalPrefix + "floorplans", false},
		{"pkg/client", "net/http", true},
		{"pkg/client", internalPrefix + "floorplans/api", false},
		{"cmd/spaceweave", internalPrefix + "misc", false},
	}
	for _, tc := range cases {
		t.Run(tc.source+"/"+tc.imported, func(t *testing.T) {
			violation := productionViolation(tc.source, tc.imported)
			if (violation == "") != tc.allowed {
				t.Fatalf("allowed=%v, violation=%q", tc.allowed, violation)
			}
		})
	}
}

func TestImportRules(t *testing.T) {
	cases := []struct {
		name, source, imported string
		allowed                bool
	}{
		{"api stdlib", "floorplans/api", "context", true},
		{"api kernel", "floorplans/api", internalPrefix + "kernel", true},
		{"api kernel helper", "floorplans/api", internalPrefix + "kernel/interval", true},
		{"api foreign contract", "floorplans/api", internalPrefix + "booking/api", false},
		{"api provider", "floorplans/api", "github.com/anthropics/anthropic-sdk-go/v2", false},
		{
			"api implementation",
			"floorplans/api",
			internalPrefix + "floorplans/processing/plan",
			false,
		},
		{"core contract", "floorplans/core", internalPrefix + "floorplans/api", true},
		{"core helper", "floorplans/core", internalPrefix + "floorplans/core/policy", true},
		{"core kernel", "floorplans/core", internalPrefix + "kernel", true},
		{"core platform store", "floorplans/core", internalPrefix + "platform/postgres", false},
		{
			"core adapter",
			"floorplans/core",
			internalPrefix + "floorplans/adapters/localimport/catalog",
			false,
		},
		{
			"core processing",
			"floorplans/core",
			internalPrefix + "floorplans/processing/plan",
			false,
		},
		{"core external", "floorplans/core", "github.com/go-chi/chi/v5", false},
		{
			"processing peer",
			"floorplans/processing/autotrace",
			internalPrefix + "floorplans/processing/floorgraph",
			true,
		},
		{
			"processing adapter",
			"floorplans/processing/autotrace",
			internalPrefix + "floorplans/adapters/jobs",
			false,
		},
		{"module app", "floorplans/adapters/jobs", internalPrefix + "app/config", false},
		{"module gateway", "floorplans/adapters/http", internalPrefix + "gateway/portal", false},
		{"cross module contract", "booking", internalPrefix + "identity/api", true},
		{"cross module constructor", "booking", internalPrefix + "identity", false},
		{
			"cross module implementation",
			"events",
			internalPrefix + "booking/adapters/postgres",
			false,
		},
		{"app constructor", "app", internalPrefix + "floorplans", true},
		{"app adapter", "app", internalPrefix + "floorplans/adapters/jobs", true},
		{"gateway contract", "gateway/portal", internalPrefix + "floorplans/api", true},
		{
			"gateway implementation",
			"gateway/portal",
			internalPrefix + "floorplans/adapters/http",
			false,
		},
		{"platform peer", "platform/httpkit", internalPrefix + "platform/buildinfo", true},
		{"platform kernel", "platform/httpkit", internalPrefix + "kernel", true},
		{"kernel stdlib", "kernel", "time", true},
		{"kernel business", "kernel", internalPrefix + "floorplans/api", false},
		{"kernel provider", "kernel", "github.com/go-chi/chi/v5", false},
		{"platform module", "platform", internalPrefix + "floorplans/api", false},
		{"unknown owner", "misc", "context", false},
		{"unknown dependency", "app", internalPrefix + "misc", false},
		{"api prefix collision", "booking", internalPrefix + "identity/apiculture", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			violation := importViolation(tc.source, tc.imported)
			if (violation == "") != tc.allowed {
				t.Fatalf("allowed=%v, violation=%q", tc.allowed, violation)
			}
		})
	}
}
