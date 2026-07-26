package originagent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
	filectx "github.com/torrischen/goat/agent/contextmgr/file"
	"github.com/torrischen/goat/agent/toolplugin"
	"github.com/torrischen/goat/agent/tools"
	"github.com/torrischen/goat/streaming"
	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/alitto/pond/v2"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
)

var _ common.Agent = (*Agent)(nil)

type Agent struct {
	mu              *sync.RWMutex
	contextManager  contextmgr.ContextManager
	skills          []string
	llmClient       model.AgenticModel
	tools           []common.Tool
	toolsMap        map[string]common.Tool
	modelMaxTokensK int
}

// NewAgent creates a tool-calling agent backed by Eino's model.AgenticModel.
//
// The agent intentionally trusts the AgenticModel contract and does not branch on
// OpenAI/Claude/Gemini-specific message quirks. Provider differences should be
// handled by the Eino agentic model implementations and provider-specific
// model.Option values passed by callers.
//
// Typical provider construction:
//
// OpenAI Responses API:
//
//	import (
//	    "github.com/cloudwego/eino-ext/components/model/agenticopenai"
//	)
//
//	llm, err := agenticopenai.NewResponsesModel(ctx, &agenticopenai.ResponsesConfig{
//	    APIKey: "sk-...",
//	    Model:  "gpt-5.2",
//	    // BaseURL is optional for OpenAI-compatible gateways.
//	    // ByAzure can be set when using Azure OpenAI.
//	})
//	agent := originagent.NewAgent(llm, 128, nil)
//
// Claude:
//
//	import (
//	    "github.com/cloudwego/eino-ext/components/model/agenticclaude"
//	)
//
//	llm, err := agenticclaude.New(ctx, &agenticclaude.Config{
//	    APIKey:    "sk-ant-...",
//	    Model:     "claude-sonnet-4-5",
//	    MaxTokens: 4096,
//	    // ByBedrock or ByGoogleVertexAI can be set for hosted Claude.
//	})
//	agent := originagent.NewAgent(llm, 128, nil)
//
// Gemini on Vertex AI:
//
//	import (
//	    "cloud.google.com/go/auth/credentials"
//	    "github.com/cloudwego/eino-ext/components/model/agenticgemini"
//	    "google.golang.org/genai"
//	    "os"
//	)
//
//	client, err := genai.NewClient(ctx, &genai.ClientConfig{
//	    Backend:  genai.BackendVertexAI,
//	    Project:  "your-gcp-project",
//	    Location: "global", // or a region such as "us-central1"
//	    // Credentials may be omitted when Application Default Credentials are available.
//	})
//
//	// Or initialize Vertex AI with service account credentials JSON.
//	credentialsJSON, err := os.ReadFile("/path/to/service-account.json")
//	if err != nil {
//	    return err
//	}
//	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
//	    Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
//	    CredentialsJSON: credentialsJSON,
//	})
//	if err != nil {
//	    return err
//	}
//	client, err = genai.NewClient(ctx, &genai.ClientConfig{
//	    Backend:     genai.BackendVertexAI,
//	    Project:     "your-gcp-project",
//	    Location:    "global",
//	    Credentials: creds,
//	})
//
//	llm, err := agenticgemini.New(ctx, &agenticgemini.Config{
//	    Client: client,
//	    Model:  "gemini-2.5-flash",
//	})
//	agent := originagent.NewAgent(llm, 128, nil)
//
// Gemini Developer API uses the same agenticgemini model with a genai client
// configured by API key instead of BackendVertexAI.
func NewAgent(
	llm model.AgenticModel,
	modelMaxTokensK int,
	manager contextmgr.ContextManager,
) *Agent {
	a := &Agent{
		mu:              &sync.RWMutex{},
		contextManager:  manager,
		llmClient:       llm,
		modelMaxTokensK: modelMaxTokensK,
		toolsMap:        make(map[string]common.Tool),
	}

	if a.contextManager == nil {
		a.contextManager = filectx.NewFileContextManager("")
	}

	a.AddTools(
		context.TODO(),
		tools.GeneratePlan(),
		tools.UpdatePlan(),
	)

	return a
}

func (a *Agent) AddTools(ctx context.Context, tool ...common.Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, t := range tool {
		if t == nil {
			continue
		}

		originalName := t.Name()
		sanitized := common.SanitizeToolName(originalName)
		if sanitized == "" {
			sanitized = "tool"
		}

		finalName := sanitized
		if _, exists := a.toolsMap[finalName]; exists {
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s_%d", sanitized, i)
				if _, exists := a.toolsMap[candidate]; !exists {
					finalName = candidate
					break
				}
			}
		}

		if finalName != originalName {
			logging.Warnf("Tool name %q sanitized to %q for LLM compatibility", originalName, finalName)
		}

		wrapped := common.WrapToolName(t, finalName)
		a.tools = append(a.tools, wrapped)
		a.toolsMap[finalName] = wrapped
	}
}

func (a *Agent) AddTool(ctx context.Context, tool common.Tool) {
	a.AddTools(ctx, tool)
}

func (a *Agent) AddSkills(ctx context.Context, exclude ...string) {
	if info, err := os.Stat(common.SkillDefaultFolder); err == nil && info.IsDir() {
		entries, err := os.ReadDir(common.SkillDefaultFolder)
		if err != nil {
			logging.Errorf("ReactAgentV2 AddSkills: failed to read skill folder %s: %v", common.SkillDefaultFolder, err)
			return
		}

		skills := make([]string, 0)
		for _, entry := range entries {
			if entry.IsDir() && !slices.Contains(exclude, entry.Name()) {
				byteContent, err := os.ReadFile(
					filepath.Join(
						common.SkillDefaultFolder,
						entry.Name(),
						common.SkillMainFile,
					),
				)
				if err != nil {
					logging.Errorf("ReactAgentV2 AddSkills: failed to read skill main file for skill %s: %v", entry.Name(), err)
					continue
				}

				header, exist := common.ExtractSkillHeader(util.ByteToString(byteContent))
				if !exist {
					logging.Errorf("ReactAgentV2 AddSkills: failed to extract description for skill %s", entry.Name())
					continue
				}

				skills = append(
					skills,
					header+"\n\n",
				)
			}
		}

		a.mu.Lock()
		a.skills = append(a.skills, skills...)
		a.mu.Unlock()

		a.AddTools(
			ctx,
			tools.LoadSkills(),
			tools.ReadSpecifiedFileInSkill(),
		)
	}
}

func (a *Agent) RegisterMCPTools(ctx context.Context, cli client.MCPClient) error {
	if ctx == nil {
		ctx = context.Background()
	}

	tools, err := common.ListMCPTools(ctx, cli)
	if err != nil {
		return err
	}

	for _, t := range tools {
		a.AddTool(ctx, t)
	}

	return nil
}

func (a *Agent) LoadSharedLibPluginTools(ctx context.Context, pluginDir ...string) error {
	plugins := make([]toolplugin.ToolPlugin, 0)
	for _, dir := range pluginDir {
		ps, err := toolplugin.LoadPluginsFromSharedLib(dir)
		if err != nil {
			logging.Errorf("LoadSharedLibPluginTools error from dir %s: %v", dir, err)
			return err
		}
		plugins = append(plugins, ps...)
	}

	for _, p := range plugins {
		a.AddTools(ctx, common.NewDefaultTool(
			p.Name(),
			p.Description(),
			p.Parameters(),
			p.Execute,
		))
	}

	return nil
}

func (a *Agent) LoadRPCPluginTools(ctx context.Context, address ...string) error {
	plugins := make([]toolplugin.ToolPlugin, 0)
	for _, addr := range address {
		ps, err := toolplugin.LoadPluginsFromRPC(addr)
		if err != nil {
			logging.Errorf("LoadRPCPluginTools error from address %s: %v", addr, err)
			return err
		}

		if err := ps.Ping(); err != nil {
			logging.Errorf("LoadRPCPluginTools ping error for plugin %s: %v", ps.Name(), err)
			continue
		}

		plugins = append(plugins, ps)
	}

	for _, p := range plugins {
		a.AddTools(ctx, common.NewDefaultTool(
			p.Name(),
			p.Description(),
			p.Parameters(),
			p.Execute,
		))
	}

	return nil
}

func (a *Agent) buildSystemPrompt(planMode bool, specialRequirements []string, skillUsageInstruction string, planUsageInstruction string) string {
	a.mu.RLock()
	skills := append([]string(nil), a.skills...)
	a.mu.RUnlock()

	return renderOriginAgentSystemPrompt(
		planMode,
		skills,
		specialRequirements,
		skillUsageInstruction,
		planUsageInstruction,
	)
}

func appendConversationMessage(
	ctx context.Context,
	manager contextmgr.ContextManager,
	contextUID common.ContextUID,
	messages *[]*schema.AgenticMessage,
	message *schema.AgenticMessage,
) error {
	*messages = append(*messages, message)
	return manager.Append(ctx, contextUID, message)
}

func commitConversationMessages(
	ctx context.Context,
	manager contextmgr.ContextManager,
	contextUID common.ContextUID,
	messages *[]*schema.AgenticMessage,
	newMessages ...*schema.AgenticMessage,
) {
	*messages = append(*messages, newMessages...)
	manager.Reset(ctx, contextUID, *messages)
}

func optimizationAdviceMessages(steps ...*common.Step) []*schema.AgenticMessage {
	messages := make([]*schema.AgenticMessage, 0, len(steps))
	seen := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if step == nil || step.IsFinalAnswer || step.OptimizationAdvice == nil {
			continue
		}

		advice := strings.TrimSpace(*step.OptimizationAdvice)
		if advice == "" {
			continue
		}
		if _, ok := seen[advice]; ok {
			continue
		}
		seen[advice] = struct{}{}

		messages = append(messages, schema.UserAgenticMessage(advice))
	}

	return messages
}

func applyUsageToStep(step *common.Step, promptTokens, cachedTokens, completionTokens int) {
	if step == nil {
		return
	}
	step.AddModelUsage(promptTokens, cachedTokens, completionTokens)
}

func responseMetaFromUsage(promptTokens, cachedTokens, completionTokens int) *schema.AgenticResponseMeta {
	if promptTokens == 0 && cachedTokens == 0 && completionTokens == 0 {
		return nil
	}
	return &schema.AgenticResponseMeta{
		TokenUsage: &schema.TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: cachedTokens,
			},
		},
	}
}

func cloneAgentDoArgs(args *common.AgentDoArgs) *common.AgentDoArgs {
	if args == nil {
		return nil
	}

	clone := *args
	clone.UserInput.Images = append([]*schema.ContentBlock(nil), args.UserInput.Images...)
	clone.SpecialRequirements = append([]string(nil), args.SpecialRequirements...)

	if args.ContextMeta != nil {
		clone.ContextMeta = make(map[common.AgentDoMetaKey]any, len(args.ContextMeta))
		for k, v := range args.ContextMeta {
			clone.ContextMeta[k] = v
		}
	}
	if args.Callbacks != nil {
		callbacks := *args.Callbacks
		clone.Callbacks = &callbacks
	}
	if args.ToolExecutionOptions != nil {
		options := *args.ToolExecutionOptions
		clone.ToolExecutionOptions = &options
	}
	if args.FinalAnswerWebhook != nil {
		webhook := *args.FinalAnswerWebhook
		if args.FinalAnswerWebhook.Headers != nil {
			webhook.Headers = make(map[string]string, len(args.FinalAnswerWebhook.Headers))
			maps.Copy(webhook.Headers, args.FinalAnswerWebhook.Headers)
		}
		clone.FinalAnswerWebhook = &webhook
	}

	return &clone
}

func (a *Agent) Do(
	ctx context.Context,
	args *common.AgentDoArgs,
	opts ...model.Option,
) (common.ContextUID, streaming.Stream[*common.Step], error) {
	args = cloneAgentDoArgs(args)
	if args == nil {
		return "", nil, fmt.Errorf("agent do args is nil")
	}

	actx := common.NewAgentContext(ctx)
	for k, v := range args.ContextMeta {
		actx.SetMeta(k, v)
	}

	if args.MaxStep <= 0 {
		args.MaxStep = 8
	}
	maxStep := args.MaxStep

	var contextUID common.ContextUID
	var messages []*schema.AgenticMessage

	systemPrompt := a.buildSystemPrompt(
		args.EnablePlanning,
		args.SpecialRequirements,
		args.SkillUsageInstruction,
		args.PlanUsageInstruction,
	)

	// Initialize or restore conversation
	if args.ContextUID == "" {
		// New conversation
		contextUID = a.contextManager.InitNew(ctx)

		// Add and store system message
		systemMessage := schema.SystemAgenticMessage(systemPrompt)
		messages = []*schema.AgenticMessage{systemMessage}
		if err := a.contextManager.Append(ctx, contextUID, systemMessage); err != nil {
			return "", nil, fmt.Errorf("failed to store system message: %w", err)
		}
	} else {
		// Continue existing conversation
		contextUID = args.ContextUID

		// Restore the managed conversation history.
		messages = a.contextManager.GetAll(ctx, contextUID)
		if len(messages) == 0 {
			systemMessage := schema.SystemAgenticMessage(systemPrompt)
			messages = []*schema.AgenticMessage{systemMessage}
			if err := a.contextManager.Append(ctx, contextUID, systemMessage); err != nil {
				return "", nil, fmt.Errorf("failed to store system message: %w", err)
			}
			logging.Infof("Agent.Do: initialized empty conversation %s", contextUID)
		} else {
			// Always update system prompt to reflect current mode and requirements
			systemMessage := schema.SystemAgenticMessage(systemPrompt)
			if messages[0].Role == schema.AgenticRoleTypeSystem {
				// Replace existing system message
				messages[0] = systemMessage
				logging.Infof("Agent.Do: updated system message for conversation %s", contextUID)
			} else {
				// Insert system message at the beginning (for legacy conversations)
				messages = append([]*schema.AgenticMessage{systemMessage}, messages...)
				logging.Infof("Agent.Do: inserted system message for conversation %s", contextUID)
			}
			// Update the managed context with the new system prompt.
			a.contextManager.Reset(ctx, contextUID, messages)

			logging.Infof("Agent.Do: Restored %d messages from conversation %s", len(messages), contextUID)
		}
	}

	// Add new user input
	userParts := []*schema.ContentBlock{common.TextBlock(args.UserInput.Text)}
	userParts = append(userParts, args.UserInput.Images...)

	userMessage := &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeUser,
		ContentBlocks: userParts,
	}

	// Store user message
	if err := appendConversationMessage(ctx, a.contextManager, contextUID, &messages, userMessage); err != nil {
		return "", nil, fmt.Errorf("failed to store user message: %w", err)
	}

	stepsUsed := 0

	// Convert tools to Eino agentic model format.
	callOpts := append([]model.Option{}, opts...)
	agenticTools := a.convertToolsToAgenticFormat(args.EnablePlanning)
	if len(agenticTools) > 0 {
		callOpts = append(callOpts, model.WithTools(agenticTools))
	}

	stepStream := streaming.NewStream[*common.Step](8)
	emitStep := func(step *common.Step) error {
		if step == nil {
			return nil
		}
		return stepStream.WriteWithContext(actx, step)
	}

	runLoop := func() error {
		writeFinalAndReturn := func() error {
			finalAnswer, promptTokens, completionTokens, cachedTokens := a.generateFinalAnswer(
				actx,
				messages,
				args.SpecialRequirements,
				args.FinalAnswerStreamingFunc,
				callOpts...,
			)
			// Create Step for callbacks (not persisted in the conversation context).
			finalStep := &common.Step{
				Thought:          "Now I know the answer.",
				Action:           "Output the final answer in observation field.",
				UseToolOrNot:     false,
				ActionInputParam: nil,
				IsFinalAnswer:    true,
			}
			applyUsageToStep(finalStep, promptTokens, cachedTokens, completionTokens)

			if args.Callbacks != nil && args.Callbacks.BeforeToolExecution != nil {
				args.Callbacks.BeforeToolExecution(actx, finalStep)
			}

			finalStep.Observation = finalAnswer

			if args.Callbacks != nil && args.Callbacks.AfterToolExecution != nil {
				args.Callbacks.AfterToolExecution(actx, finalStep)
			}

			finalAnswer = finalStep.Observation
			finalMessage := common.AssistantTextMessage(finalAnswer)
			finalMessage.ResponseMeta = responseMetaFromUsage(promptTokens, cachedTokens, completionTokens)
			if err := appendConversationMessage(actx, a.contextManager, contextUID, &messages, finalMessage); err != nil {
				logging.Errorf("Agent.Do: failed to store final answer: %v", err)
				return err
			}

			stepsUsed++

			if err := emitStep(finalStep); err != nil {
				return fmt.Errorf("failed to stream final step: %w", err)
			}

			a.sendFinalAnswerWebhook(
				actx,
				args.FinalAnswerWebhook,
				a.buildFinalAnswerWebhookPayload(contextUID, args, finalAnswer),
			)

			return nil
		}

		for {
			// Check if context is canceled
			select {
			case <-actx.Done():
				logging.Infof("Agent.Do: context canceled, stopping agent")
				return actx.Err()
			default:
			}

			// reach the max step, generate the final answer then return
			if stepsUsed >= maxStep {
				if err := writeFinalAndReturn(); err != nil {
					return err
				}
				return nil
			}

			// Use Think function: think first, then check if compression is needed
			thinkResult, err := a.Think(actx, &ThinkArgs{
				UserInput:                args.UserInput,
				SpecialRequirements:      args.SpecialRequirements,
				Compress:                 args.Compress,
				CompressionOptions:       args.CompressionOptions,
				Messages:                 messages,
				SystemPrompt:             systemPrompt,
				FinalAnswerStreamingFunc: args.FinalAnswerStreamingFunc,
			}, callOpts...)
			if err != nil {
				logging.Errorf("Agent.Do: Think error: %v", err)
				return err
			}

			// If compression happened during Think, update our state
			if thinkResult.IsCompressed {
				if len(thinkResult.CompressedMessages) > 0 {
					messages = thinkResult.CompressedMessages
					a.contextManager.Reset(ctx, contextUID, messages)
				}
			}

			// Get the raw response from Think result
			raw := thinkResult.RawResponse

			// Extract reasoning content if available
			reasoningContent := messageReasoning(raw)
			toolCalls := functionToolCalls(raw)

			// Check if context is canceled after LLM call
			select {
			case <-actx.Done():
				logging.Infof("Agent.Do: context canceled after LLM call, stopping agent")
				return actx.Err()
			default:
			}

			// Check if model wants to call tools
			if len(toolCalls) > 0 {
				assistantMessage := assistantMessageFromResponse(raw)

				toolResults := make([]*schema.FunctionToolResult, len(toolCalls))
				toolSteps := make([]*common.Step, len(toolCalls))
				mu := &sync.Mutex{}

				concurr := 1
				if args.ToolExecutionOptions != nil &&
					args.ToolExecutionOptions.EnableParallel {
					if args.ToolExecutionOptions.MaxConcurrency > 0 {
						concurr = args.ToolExecutionOptions.MaxConcurrency
					} else {
						concurr = 3
					}
				}
				p := pond.NewPool(concurr, pond.WithQueueSize(len(toolCalls)))

				for i, tc := range toolCalls {
					index := i
					f := func() {
						if tc == nil {
							return
						}
						toolName := tc.Name
						tool := a.toolsMap[toolName]

						var observation string
						var actionInputParam map[string]any

						shouldExecute := true
						if tool == nil {
							observation = "Error: Tool not found: " + toolName
							actionInputParam = map[string]any{}
							shouldExecute = false
						} else {
							var toolArgs map[string]any
							if err := sonic.UnmarshalString(tc.Arguments, &toolArgs); err != nil {
								logging.Errorf("Failed to parse tool arguments: %v", err)
								observation = "Error: Failed to parse arguments: " + err.Error()
								actionInputParam = map[string]any{}
								shouldExecute = false
							} else {
								actionInputParam = toolArgs
							}
						}

						thought := "I need to use the " + toolName + " tool to help with this task."
						if reasoningContent != "" {
							thought = reasoningContent
						}

						// Create Step for callbacks (not persisted in the conversation context).
						newStep := &common.Step{
							Thought:          thought,
							Action:           "Call tool: " + toolName,
							UseToolOrNot:     true,
							ToolName:         toolName,
							ActionInputParam: actionInputParam,
							IsFinalAnswer:    false,
						}
						// One model response can request multiple tool calls, but its
						// usage belongs to the whole batch. Attach it once so callbacks
						// that aggregate Step.Usage do not double count it.
						if index == 0 {
							applyUsageToStep(newStep, thinkResult.PromptTokens, thinkResult.CachedTokens, thinkResult.CompletionTokens)
						}

						if args.Callbacks != nil && args.Callbacks.BeforeToolExecution != nil {
							args.Callbacks.BeforeToolExecution(actx, newStep)
						}

						if tool != nil && shouldExecute {
							result := tool.Execute(actx, actionInputParam)
							observation = result.String()
							newStep.ObservationImages = result.ImageParts()
						}

						newStep.Observation = observation

						if args.Callbacks != nil && args.Callbacks.AfterToolExecution != nil {
							args.Callbacks.AfterToolExecution(actx, newStep)
						}

						// append the results
						mu.Lock()
						toolSteps[index] = newStep

						toolResults[index] = &schema.FunctionToolResult{
							CallID:  tc.CallID,
							Name:    toolName,
							Content: toolResultContentBlocks(newStep.Observation, newStep.ObservationImages),
						}
						mu.Unlock()
					}

					p.Submit(f)
				}

				p.StopAndWait()

				// Commit the complete tool-call batch only after all PPOF hooks have
				// settled, so the managed context never contains an incomplete step.
				pendingMessages := []*schema.AgenticMessage{assistantMessage}

				// Add tool results to conversation
				for _, tr := range toolResults {
					if tr == nil {
						continue
					}
					pendingMessages = append(pendingMessages, common.FunctionToolResultMessage(tr))
				}

				pendingMessages = append(pendingMessages, optimizationAdviceMessages(toolSteps...)...)
				commitConversationMessages(ctx, a.contextManager, contextUID, &messages, pendingMessages...)

				// Count the whole assistant tool-call batch as a single quota step,
				// even when the model requested multiple tool executions at once.
				stepsUsed++

				// Stream completed tool executions in the same order as the model's
				// tool calls, regardless of parallel execution completion order.
				for _, step := range toolSteps {
					if err := emitStep(step); err != nil {
						return fmt.Errorf("failed to stream tool step: %w", err)
					}
				}

				if common.ConsumeInterruptSignal(actx) {
					logging.Infof("Agent.Do: interrupt signal received, stopping agent loop for conversation %s", contextUID)
					return nil
				}

				if stepsUsed >= maxStep {
					if err := writeFinalAndReturn(); err != nil {
						return err
					}
					return nil
				}

				// Continue the loop to let model process tool results
				continue
			}

			// Model returned a final answer (no tool calls)
			// Use reasoning content as thought if available
			thought := "Based on the conversation, I can now provide a final answer."
			if reasoningContent != "" {
				thought = reasoningContent
			}

			finalAnswer := assistantText(raw)
			finalMessage := raw

			// Create Step for callbacks (not persisted in the conversation context).
			newStep := &common.Step{
				Thought:          thought,
				Action:           "Generate final answer based on context.",
				UseToolOrNot:     false,
				ToolName:         "",
				ActionInputParam: nil,
				IsFinalAnswer:    true,
			}
			applyUsageToStep(newStep, thinkResult.PromptTokens, thinkResult.CachedTokens, thinkResult.CompletionTokens)

			if args.Callbacks != nil && args.Callbacks.BeforeToolExecution != nil {
				args.Callbacks.BeforeToolExecution(actx, newStep)
			}

			newStep.Observation = finalAnswer

			if args.Callbacks != nil && args.Callbacks.AfterToolExecution != nil {
				args.Callbacks.AfterToolExecution(actx, newStep)
			}

			finalAnswer = newStep.Observation
			finalMessage = common.AssistantTextMessage(finalAnswer)
			finalMessage.ResponseMeta = raw.ResponseMeta
			if reasoningContent != "" {
				finalMessage.ContentBlocks = append([]*schema.ContentBlock{common.ReasoningBlock(reasoningContent)}, finalMessage.ContentBlocks...)
			}
			if err := appendConversationMessage(ctx, a.contextManager, contextUID, &messages, finalMessage); err != nil {
				logging.Errorf("Agent.Do: failed to store final message: %v", err)
				return err
			}

			stepsUsed++

			if err := emitStep(newStep); err != nil {
				return fmt.Errorf("failed to stream final step: %w", err)
			}

			a.sendFinalAnswerWebhook(
				actx,
				args.FinalAnswerWebhook,
				a.buildFinalAnswerWebhookPayload(contextUID, args, finalAnswer),
			)

			// This is a final answer, exit
			return nil
		}
	}

	go func() {
		defer stepStream.Close()
		if err := runLoop(); err != nil {
			logging.Errorf("Agent.Do: background run error for conversation %s: %v", contextUID, err)
		}
	}()

	return contextUID, stepStream, nil
}
