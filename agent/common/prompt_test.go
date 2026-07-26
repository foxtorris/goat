package common

import (
	"fmt"
	"strings"
	"testing"
)

func TestPromptTemplatesRemainFormattingCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		args     []any
		want     string
	}{
		{
			name:     "interim summary",
			template: AgentGenerateInterimPromptTemplate,
			args:     []any{"partial answer", "user question", "serialized step"},
			want:     buildAgentGenerateInterimPrompt("partial answer", "user question", "serialized step"),
		},
		{
			name:     "step conclusion",
			template: StepConcludePromptTemplate,
			args:     []any{"user goal", "serialized step"},
			want:     buildStepConcludePrompt("user goal", "serialized step"),
		},
		{
			name:     "maximum-step conclusion",
			template: ReachMaxStepConcludeTemplate,
			args:     []any{"partial answer", "user question", "serialized steps"},
			want:     buildReachMaxStepConcludePrompt("partial answer", "user question", "serialized steps"),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprintf(tt.template, tt.args...); got != tt.want {
				t.Fatalf("formatted template mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tt.want)
			}
		})
	}
}

func TestFillCompleteGenerateInterimPrompt(t *testing.T) {
	t.Parallel()

	step := &Step{Thought: "Inspect 100% of the input", Action: "read"}
	stepDescription, err := step.ToString()
	if err != nil {
		t.Fatalf("Step.ToString() error: %v", err)
	}

	got := FillCompleteGenerateInterimPrompt(
		"A partial result",
		AgentUserInput{Text: "What is complete?"},
		[]*Step{step},
	)

	assertCommonPromptContains(t, got,
		"## Objective",
		"## Instructions",
		"### Previous Interim Answer\nA partial result",
		"### User Input\nWhat is complete?",
		"### Steps\n"+stepDescription,
		"## Output Format",
	)
	assertCommonPromptOmits(t, got, "%!")
}

func TestFillStepConcludePromptTemplate(t *testing.T) {
	t.Parallel()

	step := &Step{Thought: "Inspect the input", Action: "read", Observation: "done"}
	stepDescription, err := step.ToString()
	if err != nil {
		t.Fatalf("Step.ToString() error: %v", err)
	}

	got := FillStepConcludePromptTemplate(
		AgentUserInput{Text: "Complete the review"},
		step,
	)

	assertCommonPromptContains(t, got,
		"## Context",
		"### Goal\nComplete the review",
		"### Step\n"+stepDescription,
		`"use_tool_or_not": true`,
	)
	assertCommonPromptOmits(t, got, "%!")
}

func TestFillCompleteReachMaxStepConcludePrompt(t *testing.T) {
	t.Parallel()

	got := FillCompleteReachMaxStepConcludePrompt(
		"Partial result",
		AgentUserInput{Text: "Give me the final answer"},
		nil,
		[]string{"Use JSON.", " ", "Keep it concise."},
	)

	assertCommonPromptContains(t, got,
		"## Important",
		"## Writing Style",
		"## Answer Quality Requirements",
		"## Interim Result (internal only, do not expose)\nPartial result",
		"## User Input\nGive me the final answer",
		"## Internal Steps (internal only, do not expose)\n",
		"## Output Requirement",
		"## Special Requirements (MUST follow)\n- Use JSON.\n- Keep it concise.",
	)
	assertCommonPromptOmits(t, got, "%!")
}

func assertCommonPromptContains(t *testing.T, promptText string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(promptText, value) {
			t.Errorf("prompt does not contain %q\n--- prompt ---\n%s", value, promptText)
		}
	}
}

func assertCommonPromptOmits(t *testing.T, promptText string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(promptText, value) {
			t.Errorf("prompt unexpectedly contains %q\n--- prompt ---\n%s", value, promptText)
		}
	}
}
