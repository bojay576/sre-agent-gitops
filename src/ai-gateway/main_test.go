package main

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestDecodeToolArguments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "object", raw: `{"query":"SELECT 1"}`, want: "SELECT 1"},
		{name: "encoded object", raw: `"{\"query\":\"SELECT 2\"}"`, want: "SELECT 2"},
		{name: "empty", raw: ``, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := decodeToolArguments(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("decodeToolArguments returned error: %v", err)
			}
			if tt.want == "" {
				if len(args) != 0 {
					t.Fatalf("expected no args, got %#v", args)
				}
				return
			}
			if got := args["query"]; got != tt.want {
				t.Fatalf("query = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestOpenAITools(t *testing.T) {
	tools := openAITools([]mcp.Tool{{
		Name:        "execute_query",
		Description: "Run SQL",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string"},
			},
			Required: []string{"query"},
		},
	}})

	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if tools[0]["type"] != "function" {
		t.Fatalf("tool type = %#v, want function", tools[0]["type"])
	}
	fn, ok := tools[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function payload has unexpected type %T", tools[0]["function"])
	}
	if fn["name"] != "execute_query" {
		t.Fatalf("function name = %#v", fn["name"])
	}
	if fn["parameters"] == nil {
		t.Fatal("function parameters missing")
	}
}
