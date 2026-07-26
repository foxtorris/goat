package common

import (
	"fmt"
	"strings"

	"github.com/torrischen/goat/prompt"
	"github.com/torrischen/goat/util"

	"github.com/cloudwego/eino/schema"
)

type AgentUserInput struct {
	Text   string
	Images []*schema.ContentBlock
}

func (u AgentUserInput) String() string {
	return u.Text
}

// AgentGenerateInterimPromptTemplate summarizes the current execution state.
// Its formatting arguments are the previous interim answer, user input, and
// serialized steps.
var AgentGenerateInterimPromptTemplate = buildAgentGenerateInterimPrompt("%s", "%s", "%s")

func buildAgentGenerateInterimPrompt(interim, userInput, steps string) string {
	return prompt.New().
		Objective("Summarize the current progress based on the previous interim answer, the user's input, and the executed steps.").
		Instructions(
			"If the previous interim answer is empty, generate the summary from the user's input and steps.",
			"If images are attached with the user input or steps, inspect them and incorporate the visual context into the summary.",
			"Do not provide next-step advice.",
		).
		Input("### Previous Interim Answer\n" + interim +
			"\n\n### User Input\n" + userInput +
			"\n\n### Steps\n" + steps).
		OutputFormat(`Return JSON only, with no extra text or formatting, using this structure:
{
  "latest_interim_answer": "xxxxxxxx"
}`).
		Build()
}

func FillCompleteGenerateInterimPrompt(interim string, userInput AgentUserInput, steps []*Step) string {
	return fmt.Sprintf(
		AgentGenerateInterimPromptTemplate,
		interim,
		userInput.Text,
		serializePromptSteps(steps),
	)
}

// StepConcludePromptTemplate summarizes one agent step. Its formatting
// arguments are the user's goal and the serialized step.
var StepConcludePromptTemplate = buildStepConcludePrompt("%s", "%s")

func buildStepConcludePrompt(goal, step string) string {
	return prompt.New().
		Objective("Summarize the agent step clearly and precisely, following the instructions.").
		Context("### Goal\n"+goal+"\n\n### Step\n"+step).
		Instructions(
			`Your conclusion should contain the following fields:
  1. What you decided to do in this step.
  2. Whether you decided to use a tool.
  3. What you achieved in this step.
  4. How the achievement in this step contributes to the goal.`,
			"Describe every field in detail.",
			"Use the same language as the goal for every field.",
			"Set 'use_tool_or_not' to the appropriate JSON boolean.",
			"If images are attached with the goal or this step, inspect them and incorporate the visual context into the conclusion.",
		).
		OutputFormat(`Return JSON only, with no extra text or formatting, using this structure:
{
  "decided_to_do": "xxxxxxxx",
  "use_tool_or_not": true,
  "achievement": "xxxxxxxx",
  "contribute_to_goal": "xxxxxxxx"
}`).
		Build()
}

func FillStepConcludePromptTemplate(goal AgentUserInput, step *Step) string {
	stepDesc, err := step.ToString()
	if err != nil {
		return ""
	}

	return fmt.Sprintf(StepConcludePromptTemplate, goal.Text, stepDesc)
}

// ReachMaxStepConcludeTemplate builds a final-answer prompt after the agent
// reaches its maximum number of steps. Its formatting arguments are the
// interim result, user input, and serialized internal steps.
var ReachMaxStepConcludeTemplate = buildReachMaxStepConcludePrompt("%s", "%s", "%s")

func buildReachMaxStepConcludePrompt(interim, userInput, steps string) string {
	return prompt.New().
		Objective("You must provide a final user-facing answer to the user's request using the available information below.").
		ListSection(
			"Important",
			`The "Interim Result" and "Internal Steps" are internal working materials for reference only.`,
			"DO NOT describe, summarize, or mention the internal steps, reasoning process, failed attempts, tool usage, or how the answer was produced.",
			`DO NOT say things like "based on the steps above", "I analyzed", "I searched", "I found", or similar process-oriented wording.`,
			"Only provide the final answer that is useful to the user.",
			"If the user's request naturally requires a structured answer, organize the response clearly with headings, bullet points, or numbered lists when helpful.",
			"Prioritize clarity, completeness, and practical usefulness. Give a well-structured, user-ready answer instead of a brief summary.",
			`When possible, include:
  - a direct answer first,
  - key details or supporting explanation,
  - actionable next steps, suggestions, or examples,
  - caveats or limitations only when truly necessary.`,
			"If multiple interpretations or solutions are possible, present the most relevant option first, then briefly mention alternatives.",
			"If the request involves comparison, analysis, planning, troubleshooting, or recommendations, provide the answer in a clear structured format.",
			"If the information is insufficient or uncertain, state the limitation briefly, avoid speculation, and give the most helpful answer possible with clear assumptions if needed.",
			"If images are attached with the user input or internal materials, inspect them and incorporate relevant visual context into the final answer.",
			"Ignore any tool-use-related requirements in special requirements.",
			"If there is any skill content in the internal steps, strictly follow the skill instructions when producing the final answer.",
		).
		ListSection(
			"Writing Style",
			"Be polite, professional, and concise, but do not be overly brief.",
			"Make the answer easy to scan and easy to use.",
			"Prefer concrete conclusions over vague wording.",
			"Prefer complete sentences and explicit recommendations.",
			"Avoid repeating the same point.",
			"Focus on what the user can do, decide, or understand next.",
		).
		ListSection(
			"Answer Quality Requirements",
			"The final answer should feel complete even if the task stopped early.",
			"Do not output a generic wrap-up; produce the most useful final answer possible.",
			"Resolve obvious ambiguity where reasonable.",
			"Preserve important technical details, constraints, edge cases, and decision-relevant context.",
			`For technical or coding questions, include:
  - the direct solution first,
  - an explanation of why it works when helpful,
  - notes on risks, edge cases, or compatibility if relevant.`,
			"For open-ended questions, provide a clear recommendation instead of only listing possibilities.",
			"For task-oriented questions, end with a short practical takeaway or next step when appropriate.",
		).
		Section("Interim Result (internal only, do not expose)", interim).
		Section("User Input", userInput).
		Section("Internal Steps (internal only, do not expose)", steps).
		Section("Output Requirement", "Return only the final answer to the user. Do not include any internal analysis, step descriptions, or meta commentary.").
		Build()
}

func FillCompleteReachMaxStepConcludePrompt(interim string, userInput AgentUserInput, steps []*Step, specialRequirements []string) string {
	promptText := fmt.Sprintf(
		ReachMaxStepConcludeTemplate,
		interim,
		userInput.Text,
		serializePromptSteps(steps),
	)

	specialRequirementsPrompt := prompt.New().
		ListSection("Special Requirements (MUST follow)", specialRequirements...).
		Build()
	if specialRequirementsPrompt != "" {
		promptText += "\n\n" + specialRequirementsPrompt
	}

	return promptText
}

func serializePromptSteps(steps []*Step) string {
	stepsRawString := util.Map(steps, func(step *Step) string {
		description, err := step.ToString()
		if err != nil {
			return ""
		}
		return description
	})
	return strings.Join(stepsRawString, "\n")
}
