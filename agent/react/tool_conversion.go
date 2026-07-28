package react

import (
	"fmt"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/tools"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// convertToolsToAgenticFormat converts agent tools to Eino agentic model tool format.
func (a *Agent) convertToolsToAgenticFormat(planMode bool) []*schema.ToolInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.tools) == 0 {
		return nil
	}

	agenticTools := make([]*schema.ToolInfo, 0, len(a.tools))
	for _, tool := range a.tools {
		if !planMode &&
			(tool.Name() == tools.InternalToolGeneratePlan || tool.Name() == tools.InternalToolUpdatePlan) {
			continue
		}

		params, err := toolParametersToJSONSchema(a.extractToolParameters(tool))
		if err != nil {
			logging.Errorf("Failed to convert tool %s parameters to JSON schema: %v", tool.Name(), err)
			params = schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
				Type:       "object",
				Properties: nil,
			})
		}

		agenticTools = append(agenticTools, &schema.ToolInfo{
			Name:        tool.Name(),
			Desc:        tool.Description(),
			ParamsOneOf: params,
		})
	}

	return agenticTools
}

// extractToolParameters extracts parameters from a tool in JSON schema format
func (a *Agent) extractToolParameters(tool common.Tool) map[string]any {
	if tool == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	// Prefer the structured parameters directly.
	if params := tool.Parameters(); params != nil {
		cast := map[string]any(params)
		// Ensure object type schemas always have properties field (required by OpenAI/Anthropic APIs)
		if schemaType, ok := cast["type"].(string); ok && schemaType == "object" {
			if _, hasProps := cast["properties"]; !hasProps {
				cast["properties"] = map[string]any{}
			}
		}
		return cast
	}

	// Fallback: Parse the JSON to extract parameters (legacy paths).
	var toolDesc map[string]any
	if err := sonic.UnmarshalString(tool.String(), &toolDesc); err != nil {
		logging.Errorf("Failed to parse tool JSON: %v", err)
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	if params, ok := toolDesc["tool_parameters"].(map[string]any); ok {
		if schemaType, ok := params["type"].(string); ok && schemaType == "object" {
			if _, hasProps := params["properties"]; !hasProps {
				params["properties"] = map[string]any{}
			}
		}
		return params
	}
	if params, ok := toolDesc["parameters"].(map[string]any); ok {
		if schemaType, ok := params["type"].(string); ok && schemaType == "object" {
			if _, hasProps := params["properties"]; !hasProps {
				params["properties"] = map[string]any{}
			}
		}
		return params
	}

	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func toolParametersToJSONSchema(params map[string]any) (*schema.ParamsOneOf, error) {
	if params == nil {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	raw, err := sonic.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	js := &jsonschema.Schema{}
	if err := sonic.Unmarshal(raw, js); err != nil {
		return nil, fmt.Errorf("unmarshal params as json schema: %w", err)
	}

	return schema.NewParamsOneOfByJSONSchema(js), nil
}
