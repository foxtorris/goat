package react

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr/ram"
)

type skillCaptureModel struct {
	mu        sync.Mutex
	inputs    [][]*schema.AgenticMessage
	responses []*schema.AgenticMessage
	calls     int
}

func (m *skillCaptureModel) Generate(
	_ context.Context,
	_ []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.AgenticMessage, error) {
	return nil, errors.New("unexpected Generate call")
}

func (m *skillCaptureModel) Stream(
	_ context.Context,
	input []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, common.CloneAgenticMessages(input))
	response := common.AssistantTextMessage("done")
	if m.calls < len(m.responses) {
		response = m.responses[m.calls]
	}
	m.calls++
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{response}), nil
}

func (m *skillCaptureModel) systemPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.inputs) == 0 {
		return ""
	}
	return messagePlainText(m.inputs[len(m.inputs)-1][0])
}

func (m *skillCaptureModel) systemPrompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, 0, len(m.inputs))
	for _, input := range m.inputs {
		if len(input) > 0 {
			result = append(result, messagePlainText(input[0]))
		}
	}
	return result
}

func TestDoUsesPerRunSkillsDirAndContextMeta(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	skillsDir := t.TempDir()
	writeTestSkill(t, skillsDir, "custom-skill", "custom skill marker")
	writeTestSkill(t, skillsDir, "excluded-skill", "excluded skill marker")

	llm := &skillCaptureModel{responses: []*schema.AgenticMessage{
		skillProbeToolCall("skills-probe-1"),
		common.AssistantTextMessage("done"),
	}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddSkills(ctx, "excluded-skill")

	var toolSkillsDir string
	agent.AddTool(ctx, common.NewDefaultTool(
		"capture_skills_dir",
		"Capture the configured skill root for a test.",
		common.NewToolParameters(),
		func(actx *common.AgentContext, _ map[string]any) common.ToolResult {
			toolSkillsDir = common.SkillsDirFromContext(actx)
			return common.NewDefaultToolResult("captured")
		},
	))
	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "use the custom skill"},
		SkillsDir: skillsDir,
		ContextMeta: map[common.AgentDoMetaKey]any{
			common.InternalToolSkillsDirMetaKey: "must-be-overridden",
		},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	events := readAllEvents(t, ctx, eventStream)
	if got := len(eventsByType[common.FinalAnswerCompletedEvent](events)); got != 1 {
		t.Fatalf("final answer event count = %d, want 1", got)
	}
	if toolSkillsDir != skillsDir {
		t.Errorf("tool skills dir = %q, want %q", toolSkillsDir, skillsDir)
	}
	prompt := llm.systemPrompt()
	if !strings.Contains(prompt, "custom skill marker") {
		t.Fatalf("system prompt does not contain custom skill header:\n%s", prompt)
	}
	if strings.Contains(prompt, "excluded skill marker") {
		t.Fatalf("system prompt contains excluded skill header:\n%s", prompt)
	}
}

func TestDoReloadsSkillsFromEachRunDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstDir := t.TempDir()
	secondDir := t.TempDir()
	writeTestSkill(t, firstDir, "first", "first directory marker")
	writeTestSkill(t, secondDir, "second", "second directory marker")

	llm := &skillCaptureModel{}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddSkills(ctx)
	for _, skillsDir := range []string{firstDir, secondDir} {
		_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
			UserInput: common.AgentUserInput{Text: "use skills"},
			SkillsDir: skillsDir,
		})
		if err != nil {
			t.Fatalf("Do(%q) error = %v", skillsDir, err)
		}
		_ = readAllEvents(t, ctx, eventStream)
	}

	prompts := llm.systemPrompts()
	if len(prompts) != 2 {
		t.Fatalf("model prompts = %d, want 2", len(prompts))
	}
	if !strings.Contains(prompts[0], "first directory marker") || strings.Contains(prompts[0], "second directory marker") {
		t.Errorf("first prompt used the wrong skills directory:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[1], "second directory marker") || strings.Contains(prompts[1], "first directory marker") {
		t.Errorf("second prompt used the wrong skills directory:\n%s", prompts[1])
	}
}

func TestDoDefaultsSkillsDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	llm := &skillCaptureModel{responses: []*schema.AgenticMessage{
		skillProbeToolCall("skills-probe-2"),
		common.AssistantTextMessage("done"),
	}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	var got string
	agent.AddTool(ctx, common.NewDefaultTool(
		"capture_skills_dir",
		"Capture the configured skill root for a test.",
		common.NewToolParameters(),
		func(actx *common.AgentContext, _ map[string]any) common.ToolResult {
			got = common.SkillsDirFromContext(actx)
			return common.NewDefaultToolResult("captured")
		},
	))
	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = readAllEvents(t, ctx, eventStream)
	if got != common.SkillDefaultFolder {
		t.Errorf("skills dir = %q, want %q", got, common.SkillDefaultFolder)
	}
}

func skillProbeToolCall(callID string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID:    callID,
				Name:      "capture_skills_dir",
				Arguments: `{}`,
			}),
		},
	}
}

func writeTestSkill(t *testing.T, root, name, marker string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + marker + "\n---\n\nInstructions.\n"
	if err := os.WriteFile(filepath.Join(dir, common.SkillMainFile), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
