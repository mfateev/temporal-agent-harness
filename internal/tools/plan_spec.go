// Plan tool specification for the update_plan intercepted tool.
//
// Maps to: Codex update_plan tool (codex-rs/core/src/tools/spec.rs)
package tools

func init() {
	RegisterSpec(SpecEntry{Name: "update_plan", Constructor: NewUpdatePlanToolSpec})
}

// NewUpdatePlanToolSpec creates the specification for the update_plan tool.
// This tool is intercepted by the workflow (not dispatched as an activity).
// It allows the LLM to maintain a visible task plan with steps and statuses.
//
// Maps to: Codex update_plan tool spec
func NewUpdatePlanToolSpec() ToolSpec {
	return ToolSpec{
		Name: "update_plan",
		Description: `Create or update a plan with steps to track progress. At most one step can be "in_progress" at a time. Use this to outline your approach before starting complex tasks, and update step statuses as you complete them.`,
		Parameters: []ToolParameter{
			{
				Name:        "explanation",
				Type:        "string",
				Description: "Optional brief explanation of the plan or current changes.",
				Required:    false,
			},
			{
				Name:        "plan",
				Type:        "array",
				Description: `Array of plan steps. Each step has a "step" (description) and "status" ("pending", "in_progress", or "completed"). At most one step should be "in_progress".`,
				Required:    true,
				Items: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"step": map[string]interface{}{
							"type":        "string",
							"description": "Description of this plan step.",
						},
						"status": map[string]interface{}{
							"type":        "string",
							"description": "Status of this step.",
							"enum":        []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"step", "status"},
				},
			},
		},
	}
}
