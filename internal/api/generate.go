// Package api is the Prefactor HTTP API client, generated from the p2
// OpenAPI document in api/openapi.json.
package api

//go:generate go run ../../tools/specnormalize ../../api/openapi.json ../../api/openapi.normalized.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../api/openapi.normalized.json
