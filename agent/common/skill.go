package common

import "strings"

const (
	SkillDefaultFolder = "skills"
	SkillMainFile      = "SKILL.md"
)

func ExtractSkillHeader(text string) (string, bool) {
	lines := strings.Split(text, "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "---" {
			start = i
			break
		}
	}
	if start == -1 {
		return "", false
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", false
	}

	return strings.Join(lines[start+1:end], "\n"), true
}
