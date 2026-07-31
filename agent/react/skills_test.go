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
	mu     sync.Mutex
	inputs [][]*schema.AgenticMessage
}

func (m *skillCaptureModel) Generate(
	_ context.Context,
	input []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, common.CloneAgenticMessages(input))
	m.mu.Unlock()
	return common.AssistantTextMessage("done"), nil
}

func (m *skillCaptureModel) Stream(
	context.Context,
	[]*schema.AgenticMessage,
	...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return nil, errors.New("unexpected Stream call")
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

	llm := &skillCaptureModel{}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddSkills(ctx, "excluded-skill")

	var callbackSkillsDir string
	_, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "use the custom skill"},
		SkillsDir: skillsDir,
		ContextMeta: map[common.AgentDoMetaKey]any{
			common.InternalToolSkillsDirMetaKey: "must-be-overridden",
		},
		Callbacks: &common.Callbacks{
			BeforeToolExecution: func(actx *common.AgentContext, _ *common.Step) {
				callbackSkillsDir = common.SkillsDirFromContext(actx)
			},
		},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	steps := readAllSteps(t, ctx, stepStream)
	if len(steps) != 1 || !steps[0].IsFinalAnswer {
		t.Fatalf("steps = %+v, want one final step", steps)
	}
	if callbackSkillsDir != skillsDir {
		t.Errorf("callback skills dir = %q, want %q", callbackSkillsDir, skillsDir)
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
		_, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
			UserInput: common.AgentUserInput{Text: "use skills"},
			SkillsDir: skillsDir,
		})
		if err != nil {
			t.Fatalf("Do(%q) error = %v", skillsDir, err)
		}
		_ = readAllSteps(t, ctx, stepStream)
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

	llm := &skillCaptureModel{}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	var got string
	_, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "hello"},
		Callbacks: &common.Callbacks{
			BeforeToolExecution: func(actx *common.AgentContext, _ *common.Step) {
				got = common.SkillsDirFromContext(actx)
			},
		},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = readAllSteps(t, ctx, stepStream)
	if got != common.SkillDefaultFolder {
		t.Errorf("skills dir = %q, want %q", got, common.SkillDefaultFolder)
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
