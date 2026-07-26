package common

import (
	"fmt"
	"strings"

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

var AgentGenerateInterimPromptTemplate = `
## Objective
Summarize the current progress based on the previous interim answer, the user's input, and the executed steps.

## Instructions
- If the previous interim answer is empty, generate the summary from the user's input and steps.
- If images are attached with the user input or steps, inspect them and incorporate the visual context into the summary.
- Output JSON using the following format:
{
  "latest_interim_answer": "xxxxxxxx",
}
- Output JSON only, with no extra text or formatting.

Previous Interim Answer:  

%s


User's Input:  
%s

Steps:  

%s

Now, begin. Do not provide next-step advice.
`

func FillCompleteGenerateInterimPrompt(interim string, userInput AgentUserInput, steps []*Step) string {
	stepsRawString := util.Map(steps, func(s *Step) string {
		desc, err := s.ToString()
		if err != nil {
			return ""
		}

		return desc
	})
	stepsDesc := strings.Join(stepsRawString, "\n")

	prompt := fmt.Sprintf(AgentGenerateInterimPromptTemplate, interim, userInput.Text, stepsDesc)

	return prompt
}

var StepConcludePromptTemplate = `
## Objective
Summarize the agent step clearly and precisely, following the instructions.

Goal:  

%s

Step:  

%s

## Instructions
- Your conclusion should contain the following fields:
 1. What you decided to do in this step.
 2. Whether you decided to use a tool or not.
 3. What you achieved in this step.
 4. To achieve the goal, how the achievement in this step contribute to the goal.
- All the fields should be in detail.
- All the fields should be in the same language as the goal.
- If images are attached with the goal or this step, inspect them and incorporate the visual context into the conclusion.
- Output JSON using the following format:
{
  "decided_to_do": "xxxxxxxx",
  "use_tool_or_not": true/false,
  "achievement": "xxxxxxxx",
  "contribute_to_goal": "xxxxxxxx"
}
- Output JSON only, with no extra text or formatting.

Now, let's start!
`

func FillStepConcludePromptTemplate(goal AgentUserInput, step *Step) string {
	stepDesc, err := step.ToString()
	if err != nil {
		return ""
	}
	prompt := fmt.Sprintf(StepConcludePromptTemplate, goal.Text, stepDesc)

	return prompt
}

var ReachMaxStepConcludeTemplate = `
## Objective
You must provide a final user-facing answer to the user's request using the available information below.

## Important
- The "Interim Result" and "Internal Steps" are internal working materials for reference only.
- DO NOT describe, summarize, or mention the internal steps, reasoning process, failed attempts, tool usage, or how the answer was produced.
- DO NOT say things like "based on the steps above", "I analyzed", "I searched", "I found", or similar process-oriented wording.
- Only provide the final answer that is useful to the user.
- If the user's request naturally requires a structured answer, organize the response clearly with headings, bullet points, or numbered lists when helpful.
- Prioritize clarity, completeness, and practical usefulness. Give a well-structured, user-ready answer instead of a brief summary.
- When possible, include:
  - a direct answer first,
  - key details or supporting explanation,
  - actionable next steps, suggestions, or examples,
  - caveats or limitations only when truly necessary.
- If multiple interpretations or solutions are possible, present the most relevant option first, then briefly mention alternatives.
- If the request involves comparison, analysis, planning, troubleshooting, or recommendations, provide the answer in a clear structured format.
- If the information is insufficient or uncertain, state the limitation briefly, avoid speculation, and give the most helpful answer possible with clear assumptions if needed.
- If images are attached with the user input or internal materials, inspect them and incorporate relevant visual context into the final answer.
- Ignore any tool-use-related requirements in special requirements.
- If there are any skills content in the internal steps, you should strictly follow the skills' instructions and use the skills to produce the final answer.

## Writing Style
- Be polite, professional, and concise, but do not be overly brief.
- Make the answer easy to scan and easy to use.
- Prefer concrete conclusions over vague wording.
- Prefer complete sentences and explicit recommendations.
- Avoid repeating the same point.
- Focus on what the user can do, decide, or understand next.

## Answer Quality Requirements
- The final answer should feel complete even if the task stopped early.
- Do not output a generic wrap-up; produce the most useful final answer possible.
- Resolve obvious ambiguity where reasonable.
- Preserve important technical details, constraints, edge cases, and decision-relevant context.
- For technical or coding questions, include:
  - the direct solution first,
  - explanation of why it works when helpful,
  - notes on risks, edge cases, or compatibility if relevant.
- For open-ended questions, provide a clear recommendation instead of only listing possibilities.
- For task-oriented questions, end with a short practical takeaway or next step when appropriate.

## Interim Result (internal only, do not expose)
%s

## User Input
%s

## Internal Steps (internal only, do not expose)
%s

## Output Requirement
Return only the final answer to the user. Do not include any internal analysis, step descriptions, or meta commentary.
`

func FillCompleteReachMaxStepConcludePrompt(interim string, userInput AgentUserInput, steps []*Step, specialRequirements []string) string {
	stepsRawString := util.Map(steps, func(s *Step) string {
		desc, err := s.ToString()
		if err != nil {
			return ""
		}

		return desc
	})
	stepsDesc := strings.Join(stepsRawString, "\n")

	prompt := fmt.Sprintf(ReachMaxStepConcludeTemplate, interim, userInput.Text, stepsDesc)

	if len(specialRequirements) > 0 {
		special := "- " + strings.Join(specialRequirements, "\n- ")

		prompt = prompt + fmt.Sprintf("\nSpecial Requirements(MUST follow):  \n\n%s", special)
	}

	return prompt
}
