package common

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestContentAndMessageHelpers(t *testing.T) {
	if got := TextBlock("user").UserInputText.Text; got != "user" {
		t.Fatalf("TextBlock text = %q", got)
	}
	if got := AssistantTextBlock("assistant").AssistantGenText.Text; got != "assistant" {
		t.Fatalf("AssistantTextBlock text = %q", got)
	}
	if got := ReasoningBlock("think").Reasoning.Text; got != "think" {
		t.Fatalf("ReasoningBlock text = %q", got)
	}
	if got := ImageURLBlock("https://example.test/image.png").UserInputImage.URL; got != "https://example.test/image.png" {
		t.Fatalf("ImageURLBlock URL = %q", got)
	}
	image := ImageURLWithDetailBlock("url", "high").UserInputImage
	if image.URL != "url" || image.Detail != schema.ImageURLDetail("high") {
		t.Fatalf("image = %+v", image)
	}
	binary := BinaryImageBlock("image/png", []byte("data")).UserInputImage
	if binary.MIMEType != "image/png" || binary.Base64Data != base64.StdEncoding.EncodeToString([]byte("data")) {
		t.Fatalf("binary image = %+v", binary)
	}
	encoded := Base64ImageBlock("image/jpeg", "encoded").UserInputImage
	if encoded.MIMEType != "image/jpeg" || encoded.Base64Data != "encoded" {
		t.Fatalf("base64 image = %+v", encoded)
	}

	roles := []schema.AgenticRoleType{schema.AgenticRoleTypeUser, schema.AgenticRoleTypeAssistant, schema.AgenticRoleTypeSystem}
	for _, role := range roles {
		if got := TextMessage(role, "text").Role; got != role {
			t.Fatalf("TextMessage(%s) role = %s", role, got)
		}
	}
	result := &schema.FunctionToolResult{}
	if got := FunctionToolResultMessage(result); got.Role != schema.AgenticRoleTypeUser || got.ContentBlocks[0].FunctionToolResult != result {
		t.Fatalf("FunctionToolResultMessage() = %+v", got)
	}

	messages := []*schema.AgenticMessage{schema.UserAgenticMessage("hello")}
	clone := CloneAgenticMessages(messages)
	clone[0] = nil
	if messages[0] == nil {
		t.Fatal("CloneAgenticMessages did not copy the slice")
	}
	if got := CloneAgenticMessages(nil); got == nil || len(got) != 0 {
		t.Fatalf("CloneAgenticMessages(nil) = %#v", got)
	}
}

func TestAgentContextMetadataAndInterrupt(t *testing.T) {
	if InternalToolPlanMetaKey.String() != "current_plan" {
		t.Fatalf("meta key = %q", InternalToolPlanMetaKey.String())
	}
	ctx := NewAgentContext(context.Background())
	ctx.SetMeta(InternalToolPlanMetaKey, "plan")
	if got := ctx.GetMeta(InternalToolPlanMetaKey); got != "plan" {
		t.Fatalf("GetMeta = %v", got)
	}
	snapshot := ctx.GetAllMeta()
	snapshot[InternalToolPlanMetaKey] = "changed"
	if ctx.GetMeta(InternalToolPlanMetaKey) != "plan" {
		t.Fatal("GetAllMeta exposed the backing map")
	}
	ctx.DeleteMeta(InternalToolPlanMetaKey)
	if ctx.GetMeta(InternalToolPlanMetaKey) != nil {
		t.Fatal("DeleteMeta did not remove value")
	}
	if ConsumeInterruptSignal(nil) {
		t.Fatal("nil context reported an interrupt")
	}
	var nilContext *AgentContext
	nilContext.signalInterrupt()
	ctx.signalInterrupt()
	if !ConsumeInterruptSignal(ctx) || ConsumeInterruptSignal(ctx) {
		t.Fatal("interrupt signal was not consumed exactly once")
	}
}

func TestUsage(t *testing.T) {
	if NewAgentUsage(0, 0, 0) != nil || (*AgentUsage)(nil).Clone() != nil {
		t.Fatal("zero or nil usage should return nil")
	}
	usage := NewAgentUsage(1, 2, 3)
	clone := usage.Clone()
	clone.Add(NewAgentUsage(4, 5, 6))
	if !reflect.DeepEqual(clone, &AgentUsage{PromptTokens: 5, CachedTokens: 7, CompletionTokens: 9}) {
		t.Fatalf("combined usage = %+v", clone)
	}
	usage.Add(nil)
	(*AgentUsage)(nil).Add(usage)
}

func TestNamesSkillsAndResults(t *testing.T) {
	if got := SanitizeToolName("hello world/工具"); got != "hello_world___" {
		t.Fatalf("SanitizeToolName = %q", got)
	}
	if SanitizeToolName("") != "" || SanitizeToolName("valid-1.name") != "valid-1.name" {
		t.Fatal("valid tool name was changed")
	}
	tool := NewDefaultTool("old", "description", nil, func(*AgentContext, map[string]any) ToolResult {
		return NewDefaultToolResult("ok")
	})
	if WrapToolName(nil, "new") != nil || WrapToolName(tool, "") != tool || WrapToolName(tool, "old") != tool {
		t.Fatal("WrapToolName changed a no-op input")
	}
	wrapped := WrapToolName(tool, "new")
	if wrapped.Name() != "new" || wrapped.Description() != "description" || wrapped.Execute(nil, nil).String() != "ok" {
		t.Fatal("wrapped tool did not delegate")
	}

	header, ok := ExtractSkillHeader("intro\n---\nname: test\ndescription: demo\n---\nbody")
	if !ok || header != "name: test\ndescription: demo" {
		t.Fatalf("ExtractSkillHeader = %q, %v", header, ok)
	}
	for _, input := range []string{"body", "---\nno ending"} {
		if _, ok := ExtractSkillHeader(input); ok {
			t.Fatalf("invalid header %q accepted", input)
		}
	}
	if ContextUID("id").String() != "id" {
		t.Fatal("ContextUID.String returned the wrong value")
	}
	result := NewDefaultToolResult("text")
	if result.String() != "text" || result.ImageParts() != nil {
		t.Fatalf("default result = %+v", result)
	}
	multi := &MultimodalToolResult{Text: "text", Images: []*schema.ContentBlock{TextBlock("image")}}
	if multi.String() != "text" || len(multi.ImageParts()) != 1 {
		t.Fatalf("multimodal result = %+v", multi)
	}
}
