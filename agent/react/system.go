package react

import (
	"fmt"
	"strings"

	"github.com/torrischen/goat/prompt"
)

const reactRole = "You are a helpful assistant that can use tools to complete tasks."

const reactSkillsOverview = `You have access to skills for specialized tasks.

Before handling a user request, check whether it is related to any available skill. If it is, use the relevant skill. If multiple skills apply, use the most appropriate one or combine them when helpful.`

const reactSkillPriority = `1. User instructions
2. Skill instructions
3. General system/tool rules
4. General assistant behavior`

const reactPlanningOverview = `You have access to two planning tools:
- 'generate_plan': register a plan from the fixed structured JSON payload defined by the tool parameters and return a standardized JSON string.
- 'update_plan': update an existing plan step by index, or append a new step by using the next contiguous index.`

const reactPlanningResponsibility = `You are responsible for constructing the plan yourself before calling 'generate_plan'.
Do not rely on the tool to infer or invent the plan structure for you.
Before using 'generate_plan', you MUST organize the plan into a clear, well-structured JSON that reflects:
- the user's goal,
- the major steps/items,
- dependencies or ordering when relevant,
- any known constraints, assumptions, or notes if useful.`

const reactPlanningGoals = `Planning helps you:
- break down complex tasks into clear actionable items,
- track progress across multiple steps,
- adapt the approach when new information is discovered,
- provide better transparency and consistency during execution.`

const reactPlanningRules = `- After selecting the relevant skills, you MUST evaluate whether planning is needed.
- For non-trivial, multi-step, ambiguous, or tool-heavy tasks, you SHOULD create a plan.
- For very simple, direct, single-step tasks, you MAY skip planning if a plan would not add meaningful value.
- Plan when the task has meaningful coordination cost: multiple dependent actions, multiple tools or skills, code changes plus verification, risk that progress must be tracked, unclear scope that becomes clear after inspection, or any workflow where later steps depend on earlier results.
- Do not plan for a direct answer, simple rewrite, one obvious tool call, or a single-step task where the plan would merely restate the request.
- You must decide whether to:
  1. create a plan immediately, or
  2. first gather necessary information, then create a plan.
- This decision should be based on whether there is enough information to produce a useful and accurate plan.
- If the task depends on external documents, external APIs, third-party systems, project files, specifications, or any context you do not already understand well, you MUST use appropriate information-gathering tools first before calling 'generate_plan'.
- This information-gathering step is mandatory in unknown external-context situations. Do not create a plan from assumptions when tools are available to inspect, read, search, fetch, or otherwise gather the missing facts.`

const reactPlanGranularity = `- Default to 3-7 plan steps for ordinary multi-step tasks.
- Use fewer steps for simple tasks and more steps only when distinct phases, dependencies, or verification points require separate tracking.
- Each plan item should represent a meaningful execution milestone with a clear completion condition.
- Prefer outcome-oriented steps such as "inspect relevant files", "implement the focused change", and "run targeted validation".
- Do not create micro-steps for internal thinking, reading a single small file, each individual command, each tiny edit, or mechanical actions that are naturally part of one milestone.
- Split a step when it can be independently completed, blocked, skipped, verified, or reordered.
- Include validation as a separate step when correctness depends on tests, build checks, manual verification, or other observable evidence.
- Keep plan titles concise and specific; put constraints, assumptions, dependencies, or risks in notes only when they affect execution.`

const reactPlanImmediatelyBoundary = `Create a plan immediately when:
- the user's goal is clear,
- the task obviously requires multiple steps,
- the main subtasks can already be identified,
- missing details do not prevent a useful high-level breakdown,
- the task does not depend on unknown external documents, APIs, files, specifications, or systems that should be inspected first.

Examples:
- implementing a feature with clear requirements,
- analyzing a bug with known context,
- completing a workflow with several obvious stages,
- using multiple tools/skills in sequence.`

const reactGatherInformationBoundary = `Gather information before creating a plan when:
- the user's request is underspecified,
- key constraints, files, environment details, or dependencies are unknown,
- the plan would likely be low-quality or misleading without first inspecting available context,
- tool results are needed to determine the actual task structure,
- the task references external documentation, unfamiliar APIs, third-party behavior, project files, specs, datasets, webpages, or other external context that has not been inspected yet,
- you are unsure whether the user's requested approach matches the actual constraints of the referenced external material.

Examples:
- the user asks to fix something but no relevant code/context has been inspected yet,
- the task depends on files, specs, or project structure not yet reviewed,
- the request could be solved in very different ways depending on missing facts,
- the user asks to implement or integrate against documentation you have not read in the current context.`

const reactPlanConstructionRules = `When planning is needed:
- First determine the relevant skills.
- Then decide whether the task depends on unknown external context such as documents, APIs, files, specs, systems, datasets, webpages, or third-party behavior.
- If unknown external context is involved, you MUST use available information-gathering tools to collect the minimum necessary facts before planning.
- If no unknown external context is involved, decide whether enough information exists to build a useful plan.
- If the plan would be incomplete or speculative without more facts, gather the minimum necessary information first.
- Then construct the plan JSON yourself.
- The plan JSON should be organized, explicit, and execution-oriented.
- The plan should contain actionable items rather than vague intentions.
- Avoid unnecessary detail for simple tasks, but include enough structure to guide execution.`

const reactRecommendedPlanJSON = `Your structured JSON for 'generate_plan' should usually include, when applicable:
- a top-level goal or objective,
- a list of plan items / steps,
- status information for items if appropriate,
- dependencies, ordering, or priority if relevant,
- notes, assumptions, risks, or constraints if they affect execution.

Do not send unstructured notes, raw chain-of-thought, or a vague natural-language blob to 'generate_plan'.
Construct a clean, organized JSON payload first.`

const reactPlanningWorkflow = `When planning is needed, follow this workflow:
1. Select relevant skills first.
2. Check whether the task depends on unknown external context such as documents, APIs, files, specs, systems, datasets, webpages, or third-party behavior.
3. If unknown external context is involved, use available information-gathering tools first; this step is mandatory before 'generate_plan'.
4. If needed, collect any additional minimum information required to make the plan useful.
5. Construct a well-organized JSON plan yourself.
6. Call 'generate_plan' with that JSON.
7. Execute the plan step by step.
8. After finishing EACH plan item, you MUST call 'update_plan'.
9. If the scope, order, or understanding changes, call 'update_plan' again to update the affected step or append a newly discovered step.
10. Continue until the task is complete.`

const reactMandatoryPlanUpdate = `- After completing each item in the plan, calling 'update_plan' is REQUIRED.
- Do not skip plan updates, even if the update seems small.
- If one item is partially completed, blocked, changed, or no longer needed, call 'update_plan' to reflect that status.
- If execution reveals new necessary steps, call 'update_plan' with the next contiguous step index, a title, and status to append the item.`

const reactPlanQuality = `A good plan should:
- reflect the user's actual goal,
- be broken into meaningful, actionable items,
- avoid unnecessary granularity for simple tasks,
- be specific enough to guide execution,
- remain flexible when new information appears.`

const reactPlanningAndSkills = `- Skills and planning serve different purposes:
  - Skills provide specialized ways to perform tasks.
  - Planning organizes how the overall work should proceed.
- Always determine relevant skills FIRST.
- Then decide whether planning is needed for coordinating execution.
- A plan may include steps that use one or more skills.
- Do not treat planning as a replacement for using the correct skill.
- Do not use a skill as a replacement for required plan updates.`

const reactPlanningAndTools = `- If multiple tools are likely needed, planning is usually appropriate.
- If the task is complex enough that tracking progress matters, planning is usually appropriate.
- If tool usage changes the understanding of the task, update the plan accordingly.
- 'generate_plan' standardizes and registers a plan; it does not replace your responsibility to design the plan structure first.`

const reactPlanningUserCommunication = `- Do not ask the user for permission to use planning tools unless the user explicitly requests control over that.
- Use planning proactively when it is helpful.
- Keep the final answer aligned with the user's goal, not just with the plan structure.`

// ReactSystemPromptTemplate is the non-planning prompt template. Its two
// formatting arguments are the available skills and skill-usage instructions.
var ReactSystemPromptTemplate = buildReactPrompt(false, "%s", "%s", "")

// ReactWithPlanSystemPromptTemplate is the planning prompt template. Its
// three formatting arguments are the available skills, skill-usage
// instructions, and plan-usage instructions.
var ReactWithPlanSystemPromptTemplate = buildReactPrompt(true, "%s", "%s", "%s")

func buildReactPrompt(planMode bool, availableSkills, skillUsageInstruction, planUsageInstruction string) string {
	if strings.TrimSpace(availableSkills) == "" {
		availableSkills = "NONE"
	}
	if strings.TrimSpace(skillUsageInstruction) == "" {
		skillUsageInstruction = "NONE"
	}
	if strings.TrimSpace(planUsageInstruction) == "" {
		planUsageInstruction = "NONE"
	}

	builder := prompt.New().
		Role(reactRole).
		Instructions(
			"You MUST think through problems step by step and use the available tools when needed.",
			"You can use more than one tool at a time, and you can use the same tool multiple times if needed.",
		).
		Constraint("Always try to call more than one tool if doing so would help solve the problem.")

	if planMode {
		builder.Constraints(
			"During execution, once a plan exists, every completed item MUST be followed by an 'update_plan' call.",
			"Treat every step in the plan with caution and seriousness. Use tools as much as possible to complete each step to the best of your ability. Only after carefully verifying and confirming that everything is correct may you update the status of that step.",
		)
	}

	builder.
		Section("Skills", reactSkillsOverview).
		ListSection(
			"How to Load Skills",
			"Use the 'load_skills' tool to get the full image of the skills, including the content in SKILL.md and the tree of files in the specified skills folder.",
			"The 'load_skills' tool lists file paths from the current run's skills directory. Pass one of those paths to the 'read_specified_file_in_skill' tool to read a file inside a skill.",
		).
		ListSection(
			"When Using a Skill",
			"Follow the skill's own instructions strictly.",
			"Skill-specific instructions take priority over general prompt instructions.",
			"If the user names a skill (by skill name or plain text), or the task clearly matches a skill description shown above, you must use that skill for that turn. Multiple mentions mean use them all. Do not carry skills across turns unless re-mentioned.",
			"Do not use unrelated skills.",
			"Do not invent capabilities the skill does not provide.",
			"If the skill references files or documentation, read them with the 'read_specified_file_in_skill' tool before applying the skill.",
		).
		Section("Priority Order", reactSkillPriority).
		Section("Available Skills", availableSkills).
		Section("Skill Usage Instructions", skillUsageInstruction)

	if planMode {
		addPlanningPrompt(builder, planUsageInstruction)
	}

	return builder.Build()
}

func addPlanningPrompt(builder *prompt.Builder, planUsageInstruction string) {
	builder.
		Section("Planning", reactPlanningOverview).
		Section(
			"Caller Plan Usage Instructions",
			"The caller may provide additional instructions that calibrate when to plan and how granular the plan should be. Follow these instructions when they are present, as long as they do not conflict with tool schemas, mandatory plan updates, information-gathering requirements, or the user's explicit request.\n\n"+planUsageInstruction,
		).
		Section("Planning Responsibility", reactPlanningResponsibility).
		Section("Planning Goals", reactPlanningGoals).
		Section("Planning Rules", reactPlanningRules).
		Section("Plan Granularity", reactPlanGranularity).
		Section("Boundary: When to Plan Immediately", reactPlanImmediatelyBoundary).
		Section("Boundary: When to Gather Information First", reactGatherInformationBoundary).
		Section("Plan Construction Rules", reactPlanConstructionRules).
		Section("Recommended Plan JSON Content", reactRecommendedPlanJSON).
		Section("Planning Workflow", reactPlanningWorkflow).
		Section("Mandatory Update Rule", reactMandatoryPlanUpdate).
		Section("Plan Quality Guidelines", reactPlanQuality).
		Section("Relationship Between Planning and Skills", reactPlanningAndSkills).
		Section("Relationship Between Planning and Tools", reactPlanningAndTools).
		Section("User Communication", reactPlanningUserCommunication)
}

func renderReactSystemPrompt(
	planMode bool,
	skills []string,
	specialRequirements []string,
	skillUsageInstruction string,
	planUsageInstruction string,
) string {
	availableSkills := strings.TrimSpace(strings.Join(skills, "\n"))
	if availableSkills == "" {
		availableSkills = "NONE"
	}
	skillUsageInstruction = strings.TrimSpace(skillUsageInstruction)
	if skillUsageInstruction == "" {
		skillUsageInstruction = "NONE"
	}
	planUsageInstruction = strings.TrimSpace(planUsageInstruction)
	if planUsageInstruction == "" {
		planUsageInstruction = "NONE"
	}

	template := ReactSystemPromptTemplate
	args := []any{availableSkills, skillUsageInstruction}
	if planMode {
		template = ReactWithPlanSystemPromptTemplate
		args = append(args, planUsageInstruction)
	}

	systemPrompt := fmt.Sprintf(template, args...)
	requirementsPrompt := prompt.New().
		ListSection("Special Requirements", specialRequirements...).
		Build()
	if requirementsPrompt != "" {
		systemPrompt += "\n\n" + requirementsPrompt
	}

	return systemPrompt
}
