package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// BreakdownTool receives a structured task breakdown from the breakdown agent.
// The LLM calls this tool with properly formatted JSON (guaranteed valid by the
// tool-calling system), eliminating free-text JSON parsing issues.
type BreakdownTool struct{}

func (t BreakdownTool) ID() string { return "submit_task_breakdown" }

func (t BreakdownTool) Description() string {
	return "Submit the task breakdown. Call this with the complete JSON array of task definitions after analyzing the plan. Do NOT output the JSON as free text — use this tool instead."
}

func (t BreakdownTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tasks": {
				"type": "array",
				"description": "Ordered array of task definitions to implement the plan",
				"items": {
					"type": "object",
					"properties": {
						"title": {
							"type": "string",
							"description": "Concise imperative title, e.g. 'Add authentication middleware'"
						},
						"description": {
							"type": "string",
							"description": "Detailed implementation notes referencing specific files, functions, and patterns"
						},
						"dependencies": {
							"type": "array",
							"items": { "type": "integer" },
							"maxItems": 1,
							"description": "0-based index of the single task this task depends on. At most ONE dependency is allowed — use linear chains (A→B→C), never fan-in (A,B→C). Leave empty for tasks with no prerequisites."
						},
						"effort": {
							"type": "string",
							"enum": ["S", "M", "L", "XL"],
							"description": "Estimated effort: S=tiny, M=medium, L=large, XL=extra-large"
						},
						"complexity": {
							"type": "string",
							"enum": ["low", "medium", "high"],
							"description": "Implementation complexity"
						},
						"orderIndex": {
							"type": "integer",
							"description": "Suggested execution order starting at 0"
						}
					},
					"required": ["title", "description", "dependencies", "effort", "complexity", "orderIndex"]
				}
			}
		},
		"required": ["tasks"]
	}`)
}

// BreakdownInput matches the tool's parameter schema.
type BreakdownInput struct {
	Tasks []struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Dependencies []int  `json:"dependencies"`
		Effort       string `json:"effort"`
		Complexity   string `json:"complexity"`
		OrderIndex   int    `json:"orderIndex"`
	} `json:"tasks"`
}

func (t BreakdownTool) Execute(_ context.Context, args json.RawMessage, _ Context) (Result, error) {
	var input BreakdownInput
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse breakdown input: %w", err)
	}
	if len(input.Tasks) == 0 {
		return Result{}, fmt.Errorf("at least one task is required")
	}
	return Result{
		Title:  fmt.Sprintf("Breakdown: %d tasks", len(input.Tasks)),
		Output: fmt.Sprintf("Task breakdown submitted with %d tasks.", len(input.Tasks)),
	}, nil
}
