package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torrischen/goat/agent/common"
)

func TestSkillToolsUseDirectoryFromAgentContext(t *testing.T) {
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, common.SkillMainFile),
		[]byte("---\ndescription: Review code.\n---\n\nUse the reference.\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "reference.md"), []byte("reference content"), 0o644); err != nil {
		t.Fatalf("WriteFile(reference.md) error = %v", err)
	}

	actx := common.NewAgentContext(t.Context())
	actx.SetMeta(common.InternalToolSkillsDirMetaKey, skillsDir)

	loaded := LoadSkills().Execute(actx, map[string]any{"skills": []any{"review"}}).String()
	if !strings.Contains(loaded, "Use the reference.") {
		t.Errorf("LoadSkills() result does not contain SKILL.md content: %s", loaded)
	}
	if !strings.Contains(loaded, filepath.Join(skillDir, "reference.md")) {
		t.Errorf("LoadSkills() result does not contain custom skill path: %s", loaded)
	}

	read := ReadSpecifiedFileInSkill().Execute(actx, map[string]any{"path": "review/reference.md"}).String()
	if read != "reference content" {
		t.Errorf("ReadSpecifiedFileInSkill() = %q, want reference content", read)
	}
}

func TestReadSpecifiedFileInSkillRejectsPathOutsideDirectory(t *testing.T) {
	parent := t.TempDir()
	skillsDir := filepath.Join(parent, "skills")
	if err := os.Mkdir(skillsDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	actx := common.NewAgentContext(t.Context())
	actx.SetMeta(common.InternalToolSkillsDirMetaKey, skillsDir)
	result := ReadSpecifiedFileInSkill().Execute(actx, map[string]any{"path": outside}).String()
	if !strings.Contains(result, "outside skills directory") {
		t.Fatalf("ReadSpecifiedFileInSkill() = %q, want path rejection", result)
	}
}
