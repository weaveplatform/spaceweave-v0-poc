// Package api embeds the versioned REST contracts. Edit src fragments and run make generate.
package api

import _ "embed"

// Product is the bundled business contract used by UI, CLI and automation.
//
//go:embed api.openapi.yaml
var Product []byte

// Operator is the separate installation operations contract.
//
//go:embed operator.openapi.yaml
var Operator []byte
