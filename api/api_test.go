package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/spaceweave/api"
	"github.com/weaveplatform/spaceweave/internal/devtools/openapibundle"
)

func TestCommittedContracts(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		document     []byte
	}{
		{"api", "/api/v1", api.Product}, {"operator", "/operator/v1", api.Operator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundled, err := openapibundle.Bundle("src/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bundled, tc.document) {
				t.Fatal("contract bundle is stale; run make generate")
			}
			doc, err := openapi3.NewLoader().LoadFromData(tc.document)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(doc.Servers) != 1 || doc.Servers[0].URL != tc.prefix {
				t.Fatal("wrong surface prefix")
			}
			seen := map[string]bool{}
			for path, item := range doc.Paths.Map() {
				for method, operation := range item.Operations() {
					if operation.OperationID == "" || seen[operation.OperationID] {
						t.Fatalf("unstable/duplicate operation ID %s %s", method, path)
					}
					seen[operation.OperationID] = true
					if operation.Responses.Status(500) == nil ||
						operation.Responses.Status(400) == nil {
						t.Fatalf("missing problem responses: %s", path)
					}
					if tc.name == "operator" && method != "GET" {
						t.Fatalf("initial operator contract must be read-only: %s", path)
					}
				}
			}
			if tc.name == "operator" && (len(seen) != 1 || !seen["getRuntime"]) {
				t.Fatal("operator API contains business operations")
			}
		})
	}
}

func TestProductMutationHeaderIsPartOfContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.Product)
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if method != "POST" && method != "PUT" && method != "DELETE" {
				continue
			}
			found := false
			for _, param := range op.Parameters {
				p := param.Value
				if p != nil && p.In == "header" && p.Name == "X-Spaceweave" {
					found = p.Required && p.Schema != nil && len(p.Schema.Value.Enum) == 1 &&
						p.Schema.Value.Enum[0] == "1"
				}
			}
			if !found {
				t.Fatalf("mutation guard missing from %s %s", method, path)
			}
		}
	}
}
