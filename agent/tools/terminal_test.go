package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torrischen/goat/agent/common"
)

func TestTerminalRunsCommandInWorkdir(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	result := Terminal().Execute(common.NewAgentContext(context.Background()), map[string]any{
		"command": "printf 'hello'; pwd",
		"workdir": workdir,
	})

	if !strings.Contains(result.String(), "Process exited with code 0") {
		t.Fatalf("result = %q, want successful exit", result.String())
	}
	if !strings.Contains(result.String(), "hello"+workdir) &&
		!strings.Contains(result.String(), "hello\n"+workdir) {
		t.Fatalf("result = %q, want command output and workdir", result.String())
	}
}

func TestTerminalReturnsNonZeroExitAndStderr(t *testing.T) {
	t.Parallel()

	result := Terminal().Execute(common.NewAgentContext(context.Background()), map[string]any{
		"command": "printf 'failure' >&2; exit 7",
	})

	if !strings.Contains(result.String(), "Process exited with code 7") {
		t.Fatalf("result = %q, want exit code 7", result.String())
	}
	if !strings.Contains(result.String(), "failure") {
		t.Fatalf("result = %q, want stderr", result.String())
	}
}

func TestTerminalTimesOut(t *testing.T) {
	t.Parallel()

	result := Terminal().Execute(common.NewAgentContext(context.Background()), map[string]any{
		"command":    "sleep 1",
		"timeout_ms": 20,
	})

	if !strings.Contains(result.String(), "Process timed out after 20 ms") {
		t.Fatalf("result = %q, want timeout", result.String())
	}
}

func TestTerminalSchema(t *testing.T) {
	t.Parallel()

	tool := Terminal()
	if tool.Name() != InternalToolShellCommand {
		t.Fatalf("tool.Name() = %q, want %q", tool.Name(), InternalToolShellCommand)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	for _, name := range []string{"command", "workdir", "timeout_ms", "max_output_bytes"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("missing property %q", name)
		}
	}
}

func TestTerminalOutputTruncationKeepsHeadAndTail(t *testing.T) {
	t.Parallel()

	output := "HEAD" + strings.Repeat("x", 2_000) + "TAIL"
	buffer := newCommandOutputBuffer(1_000)
	if _, err := buffer.Write([]byte(output[:800])); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := buffer.Write([]byte(output[800:])); err != nil {
		t.Fatalf("second write: %v", err)
	}
	truncated := buffer.String()
	if !strings.HasPrefix(truncated, "HEAD") {
		t.Fatalf("truncated output lost head: %q", truncated[:20])
	}
	if !strings.HasSuffix(truncated, "TAIL") {
		t.Fatalf("truncated output lost tail: %q", truncated[len(truncated)-20:])
	}
	if !strings.Contains(truncated, "bytes truncated") {
		t.Fatalf("truncated output missing marker")
	}
	if len(truncated) > 1_000 {
		t.Fatalf("len(truncated) = %d, want at most 1000", len(truncated))
	}
}

func TestTerminalRejectsMissingCommand(t *testing.T) {
	t.Parallel()

	result := Terminal().Execute(nil, map[string]any{
		"workdir": filepath.Clean("."),
	})
	if result.String() != "command parameter is missing or invalid." {
		t.Fatalf("result = %q", result.String())
	}
}
