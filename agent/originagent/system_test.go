package originagent

import (
	"fmt"
	"strings"
	"testing"
)

func TestOriginAgentPromptTemplatesRemainFormattingCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		args     []any
		want     string
	}{
		{
			name:     "without planning",
			template: OriginAgentSystemPromptTemplate,
			args:     []any{"code-review skill", "Use matching skills."},
			want:     buildOriginAgentPrompt(false, "code-review skill", "Use matching skills.", ""),
		},
		{
			name:     "with planning",
			template: OriginAgentWithPlanSystemPromptTemplate,
			args:     []any{"code-review skill", "Use matching skills.", "Plan complex changes."},
			want:     buildOriginAgentPrompt(true, "code-review skill", "Use matching skills.", "Plan complex changes."),
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

func TestRenderOriginAgentSystemPromptWithoutPlanning(t *testing.T) {
	t.Parallel()

	got := renderOriginAgentSystemPrompt(
		false,
		[]string{"  code-review skill  ", "test skill\n"},
		[]string{"Return JSON.", " ", "Keep the answer concise."},
		"  Prefer the narrowest matching skill.  ",
		"this must not be rendered",
	)

	assertPromptContains(t, got,
		"## Role\n"+originAgentRole,
		"## Available Skills\ncode-review skill  \ntest skill",
		"## Skill Usage Instructions\nPrefer the narrowest matching skill.",
		"## Special Requirements\n- Return JSON.\n- Keep the answer concise.",
	)
	assertPromptOmits(t, got,
		"## Planning",
		"this must not be rendered",
		"%!",
	)
}

func TestRenderOriginAgentSystemPromptWithPlanningAndDefaults(t *testing.T) {
	t.Parallel()

	got := renderOriginAgentSystemPrompt(true, nil, nil, " \n", "")

	assertPromptContains(t, got,
		"## Available Skills\nNONE",
		"## Skill Usage Instructions\nNONE",
		"## Planning\n"+originAgentPlanningOverview,
		"## Caller Plan Usage Instructions",
		"the user's explicit request.\n\nNONE",
		"## Mandatory Update Rule",
	)
	assertPromptOmits(t, got,
		"## Special Requirements",
		"%!",
	)
}

func assertPromptContains(t *testing.T, prompt string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(prompt, value) {
			t.Errorf("prompt does not contain %q\n--- prompt ---\n%s", value, prompt)
		}
	}
}

func assertPromptOmits(t *testing.T, prompt string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(prompt, value) {
			t.Errorf("prompt unexpectedly contains %q\n--- prompt ---\n%s", value, prompt)
		}
	}
}
