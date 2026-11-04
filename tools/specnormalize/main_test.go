package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeMovesBodyParametersIntoRequestBody(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
  "paths": {
    "/api/v1/pfid/generate": {
      "post": {
        "parameters": [
          {"in": "body", "name": "count", "required": true, "schema": {"type": "integer"}},
          {"in": "body", "name": "account_id", "required": false, "schema": {"type": "string"}},
          {"in": "query", "name": "start_time"}
        ]
      }
    }
  }
}`), &doc); err != nil {
		t.Fatal(err)
	}

	if err := normalize(doc); err != nil {
		t.Fatal(err)
	}

	op := doc["paths"].(map[string]any)["/api/v1/pfid/generate"].(map[string]any)["post"].(map[string]any)

	wantParams := []any{map[string]any{"in": "query", "name": "start_time", "schema": map[string]any{"type": "string"}}}
	if !reflect.DeepEqual(op["parameters"], wantParams) {
		t.Fatalf("parameters = %#v", op["parameters"])
	}

	wantBody := map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count":      map[string]any{"type": "integer"},
						"account_id": map[string]any{"type": "string"},
					},
					"required": []any{"count"},
				},
			},
		},
	}
	if !reflect.DeepEqual(op["requestBody"], wantBody) {
		t.Fatalf("requestBody = %#v", op["requestBody"])
	}
}

func TestNormalizeRejectsBodyParametersBesideARequestBody(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{"/x": map[string]any{"post": map[string]any{
		"requestBody": map[string]any{},
		"parameters":  []any{map[string]any{"in": "body", "name": "a"}},
	}}}}

	if err := normalize(doc); err == nil {
		t.Fatal("normalize succeeded, want an error")
	}
}

func TestNormalizeDropsTheNullBranchOfParameters(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{"/x": map[string]any{"get": map[string]any{
		"parameters": []any{map[string]any{"in": "query", "name": "environment_id", "schema": map[string]any{
			"description": "Environment",
			"oneOf": []any{
				map[string]any{"$ref": "#/components/schemas/Environment_Id"},
				map[string]any{"$ref": "#/components/schemas/Null"},
			},
		}}},
	}}}}

	if err := normalize(doc); err != nil {
		t.Fatal(err)
	}

	param := doc["paths"].(map[string]any)["/x"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)[0].(map[string]any)
	want := map[string]any{"$ref": "#/components/schemas/Environment_Id", "description": "Environment"}
	if !reflect.DeepEqual(param["schema"], want) {
		t.Fatalf("schema = %#v", param["schema"])
	}
}
