// Command complex_agent demonstrates a multi-step OpenAI agent with planning,
// parallel tool execution, run-scoped metadata, callbacks, context compression,
// final-answer streaming, and token accounting.
//
// The tools use deterministic in-memory incident data so the example only
// needs an OpenAI API key and never touches a production system.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	"github.com/torrischen/goat/agent/react"
	"github.com/torrischen/goat/streaming"
)

const (
	defaultQuestion = `Investigate the checkout conversion drop in the EU region during the last 30 minutes.
Correlate service metrics, dependency health, recent deployments, and known incidents.
Identify the most likely root cause, cite the evidence, and propose immediate mitigation plus follow-up actions.`
	regionMetaKey common.AgentDoMetaKey = "region"
)

var outputMu sync.Mutex

func main() {
	question := flag.String("question", defaultQuestion, "incident question for the agent")
	timeout := flag.Duration("timeout", 3*time.Minute, "maximum runtime")
	flag.Parse()

	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	llm, err := newOpenAIModel(ctx)
	if err != nil {
		log.Fatalf("create OpenAI model: %v", err)
	}

	agent := react.NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddTools(ctx,
		serviceMetricsTool(),
		dependencyHealthTool(),
		deploymentHistoryTool(),
		incidentSearchTool(),
	)

	fmt.Printf("Model: %s\nQuestion: %s\n\n", envOr("OPENAI_MODEL", "gpt-5.2"), *question)

	contextUID, steps, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: *question},
		ContextMeta: map[common.AgentDoMetaKey]any{
			regionMetaKey: "eu-west",
		},
		MaxStep:        16,
		EnablePlanning: true,
		PlanUsageInstruction: "Use a concise incident-investigation plan. Gather independent evidence in parallel, " +
			"update every plan item, and distinguish facts from hypotheses.",
		ToolExecutionOptions: &common.ToolExecutionOptions{
			EnableParallel: true,
			MaxConcurrency: 4,
		},
		Compress: true,
		CompressionOptions: common.CompressionOptions{
			Strategy:       common.CompressionStrategyPrecise,
			RecentMessages: 12,
		},
		SpecialRequirements: []string{
			"Treat tool output as the only source of operational facts.",
			"Do not claim that a mitigation was executed; this agent has read-only tools.",
			"Present evidence, confidence, immediate mitigation, and follow-up actions.",
		},
		Callbacks: &common.Callbacks{
			BeforeToolExecution: func(_ *common.AgentContext, step *common.Step) {
				if !step.IsFinalAnswer {
					printLocked("[start] %-24s input=%v\n", step.ToolName, step.ActionInputParam)
				}
			},
			AfterToolExecution: func(_ *common.AgentContext, step *common.Step) {
				if !step.IsFinalAnswer {
					printLocked("[done ] %-24s %s\n", step.ToolName, abbreviate(step.Observation, 180))
				}
			},
		},
		FinalAnswerStreamingFunc: func(_ context.Context, chunk []byte) error {
			printLocked("%s", chunk)
			return nil
		},
	})
	if err != nil {
		log.Fatalf("start agent: %v", err)
	}

	var promptTokens, cachedTokens, completionTokens, toolSteps int
	finalStarted := false
	for {
		step, readErr := steps.ReadWithContext(ctx)
		if errors.Is(readErr, streaming.ErrStreamClosed) {
			break
		}
		if readErr != nil {
			log.Fatalf("read agent stream: %v", readErr)
		}
		if step.Usage != nil {
			promptTokens += step.Usage.PromptTokens
			cachedTokens += step.Usage.CachedTokens
			completionTokens += step.Usage.CompletionTokens
		}
		if step.IsFinalAnswer {
			finalStarted = true
		} else if step.UseToolOrNot {
			toolSteps++
		}
	}

	if finalStarted {
		fmt.Println()
	}
	fmt.Printf("\nConversation: %s\n", contextUID)
	fmt.Printf("Tool steps: %d\n", toolSteps)
	fmt.Printf("Token usage: prompt=%d cached=%d completion=%d\n", promptTokens, cachedTokens, completionTokens)
}

func newOpenAIModel(ctx context.Context) (*agenticopenai.ResponsesModel, error) {
	config := &agenticopenai.ResponsesConfig{
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   envOr("OPENAI_MODEL", "gpt-5.6-terra"),
	}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		config.BaseURL = baseURL
	}
	return agenticopenai.NewResponsesModel(ctx, config)
}

func serviceMetricsTool() common.Tool {
	return common.NewDefaultTool(
		"get_service_metrics",
		"Return read-only golden signals and business metrics for one service in the active region.",
		common.NewToolParameters(
			common.ToolProperty{Name: "service", Type: "string", Required: true, Description: "Service name: checkout-api, payment-api, or inventory-api."},
			common.ToolProperty{Name: "window_minutes", Type: "integer", Required: true, Description: "Lookback window in minutes."},
		),
		func(ctx *common.AgentContext, input map[string]any) common.ToolResult {
			service, ok := requiredString(input, "service")
			if !ok {
				return toolError("service must be a non-empty string")
			}
			data := map[string]any{
				"checkout-api":  map[string]any{"request_rate_rps": 842, "error_rate_pct": 8.7, "p95_latency_ms": 1840, "conversion_pct": 41.2, "baseline_conversion_pct": 63.8, "error_peak_started_at": "2026-07-27T14:42:00Z"},
				"payment-api":   map[string]any{"request_rate_rps": 355, "error_rate_pct": 0.4, "p95_latency_ms": 240, "authorization_success_pct": 97.9},
				"inventory-api": map[string]any{"request_rate_rps": 517, "error_rate_pct": 0.2, "p95_latency_ms": 128, "availability_pct": 99.98},
			}
			metrics, exists := data[service]
			if !exists {
				return toolError("unknown service: " + service)
			}
			return jsonResult(map[string]any{
				"source": "metrics-snapshot", "region": regionFrom(ctx), "service": service,
				"window_minutes": input["window_minutes"], "metrics": metrics,
			})
		},
	)
}

func dependencyHealthTool() common.Tool {
	return common.NewDefaultTool(
		"get_dependency_health",
		"Return health checks for the direct dependencies of a service.",
		common.NewToolParameters(common.ToolProperty{
			Name: "service", Type: "string", Required: true, Description: "Service whose dependencies should be checked.",
		}),
		func(ctx *common.AgentContext, input map[string]any) common.ToolResult {
			service, ok := requiredString(input, "service")
			if !ok {
				return toolError("service must be a non-empty string")
			}
			if service != "checkout-api" {
				return jsonResult(map[string]any{"source": "dependency-monitor", "region": regionFrom(ctx), "service": service, "dependencies": []any{}})
			}
			return jsonResult(map[string]any{
				"source": "dependency-monitor", "region": regionFrom(ctx), "service": service,
				"dependencies": []map[string]any{
					{"name": "payment-api", "status": "healthy", "p95_latency_ms": 240},
					{"name": "inventory-api", "status": "healthy", "p95_latency_ms": 128},
					{"name": "feature-flag-gateway", "status": "degraded", "error_rate_pct": 12.6, "affected_route": "/v1/evaluate/batch", "started_at": "2026-07-27T14:43:00Z"},
				},
			})
		},
	)
}

func deploymentHistoryTool() common.Tool {
	return common.NewDefaultTool(
		"get_recent_deployments",
		"Return recent application and configuration deployments for a service. This tool is read-only.",
		common.NewToolParameters(
			common.ToolProperty{Name: "service", Type: "string", Required: true, Description: "Service name."},
			common.ToolProperty{Name: "limit", Type: "integer", Required: false, Description: "Maximum records to return."},
		),
		func(ctx *common.AgentContext, input map[string]any) common.ToolResult {
			service, ok := requiredString(input, "service")
			if !ok {
				return toolError("service must be a non-empty string")
			}
			deployments := []map[string]any{}
			if service == "checkout-api" {
				deployments = append(deployments,
					map[string]any{"id": "cfg-8841", "type": "configuration", "version": "checkout-flags-2026.07.27.3", "completed_at": "2026-07-27T14:40:31Z", "change": "Enabled batched feature evaluation for 100% of EU traffic", "actor": "release-automation"},
					map[string]any{"id": "app-5519", "type": "application", "version": "checkout-api-4.18.2", "completed_at": "2026-07-26T09:15:00Z", "change": "Logging-only release", "actor": "release-automation"},
				)
			}
			return jsonResult(map[string]any{"source": "deployment-registry", "region": regionFrom(ctx), "service": service, "deployments": deployments})
		},
	)
}

func incidentSearchTool() common.Tool {
	return common.NewDefaultTool(
		"search_known_incidents",
		"Search the read-only incident catalog by query and return potentially related active incidents.",
		common.NewToolParameters(
			common.ToolProperty{Name: "query", Type: "string", Required: true, Description: "Keywords describing symptoms, services, or dependencies."},
			common.ToolProperty{Name: "limit", Type: "integer", Required: false, Description: "Maximum incidents to return."},
		),
		func(ctx *common.AgentContext, input map[string]any) common.ToolResult {
			query, ok := requiredString(input, "query")
			if !ok {
				return toolError("query must be a non-empty string")
			}
			return jsonResult(map[string]any{
				"source": "incident-catalog", "region": regionFrom(ctx), "query": query,
				"incidents": []map[string]any{{
					"id": "INC-2471", "status": "investigating", "title": "Elevated batch-evaluation errors in feature-flag gateway",
					"started_at": "2026-07-27T14:43:00Z", "regions": []string{"eu-west"},
					"workaround": "Disable batched feature evaluation and use the single-evaluation endpoint.",
				}},
			})
		},
	)
}

func requiredString(input map[string]any, key string) (string, bool) {
	value, ok := input[key].(string)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func regionFrom(ctx *common.AgentContext) string {
	region, _ := ctx.GetMeta(regionMetaKey).(string)
	if region == "" {
		return "unknown"
	}
	return region
}

func jsonResult(value any) common.ToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return toolError(err.Error())
	}
	return common.NewDefaultToolResult(string(data))
}

func toolError(message string) common.ToolResult {
	return jsonResultWithoutError(map[string]any{"error": message})
}

func jsonResultWithoutError(value any) common.ToolResult {
	data, _ := json.Marshal(value)
	return common.NewDefaultToolResult(string(data))
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func abbreviate(value string, max int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func printLocked(format string, args ...any) {
	outputMu.Lock()
	defer outputMu.Unlock()
	fmt.Printf(format, args...)
}
