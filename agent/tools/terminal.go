package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/torrischen/goat/agent/common"
)

const (
	InternalToolShellCommand = "shell_command"

	defaultCommandTimeout   = 10 * time.Second
	maximumCommandTimeout   = 120 * time.Second
	defaultMaxOutputBytes   = 40_000
	maximumMaxOutputBytes   = 400_000
	minimumMaxOutputBytes   = 1_000
	truncationBoundaryBytes = 256
)

// Terminal returns a Codex-style shell command tool adapted to React's
// common.Tool interface. Commands run synchronously; PTY sessions, sandbox
// approvals, and interactive stdin are intentionally left to the host process.
func Terminal() common.Tool {
	return &common.DefaultTool{
		ToolName: InternalToolShellCommand,
		ToolDescription: `Runs a shell command and returns its combined stdout and stderr.
Use this tool for repository inspection, such as listing files, searching with rg, and reading files with sed or cat.
Always set workdir when the repository directory is known. Prefer rg and rg --files over grep and find.
Commands run synchronously without a PTY, approval flow, or sandbox supplied by this tool.`,
		ToolParameters: common.NewToolParameters(
			common.ToolProperty{
				Name:        "command",
				Type:        "string",
				Required:    true,
				Description: "Shell command to execute.",
			},
			common.ToolProperty{
				Name:        "workdir",
				Type:        "string",
				Description: "Working directory for the command. Defaults to the current process directory.",
			},
			common.ToolProperty{
				Name:        "timeout_ms",
				Type:        "integer",
				Description: "Maximum command runtime in milliseconds. Defaults to 10000 and is capped at 120000.",
			},
			common.ToolProperty{
				Name:        "max_output_bytes",
				Type:        "integer",
				Description: "Maximum returned output size in bytes. Defaults to 40000 and is capped at 400000.",
			},
		),
		F: executeShellCommand,
	}
}

// ShellCommand is an explicit alias for Terminal.
func ShellCommand() common.Tool {
	return Terminal()
}

func executeShellCommand(actx *common.AgentContext, inputs map[string]any) common.ToolResult {
	command, ok := stringInput(inputs, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return common.NewDefaultToolResult("command parameter is missing or invalid.")
	}

	workdir, _ := stringInput(inputs, "workdir")
	timeout, err := durationInput(inputs, "timeout_ms", defaultCommandTimeout, maximumCommandTimeout)
	if err != nil {
		return common.NewDefaultToolResult(err.Error())
	}
	maxOutputBytes, err := integerInput(
		inputs,
		"max_output_bytes",
		defaultMaxOutputBytes,
		minimumMaxOutputBytes,
		maximumMaxOutputBytes,
	)
	if err != nil {
		return common.NewDefaultToolResult(err.Error())
	}

	parent := context.Background()
	if actx != nil && actx.Context != nil {
		parent = actx.Context
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.CommandContext(ctx, shell, "-lc", command)
	if workdir != "" {
		cmd.Dir = workdir
	}

	startedAt := time.Now()
	output := newCommandOutputBuffer(maxOutputBytes)
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	elapsed := time.Since(startedAt)

	exitCode := 0
	if runErr != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	status := fmt.Sprintf("Wall time: %.4f seconds\nProcess exited with code %d", elapsed.Seconds(), exitCode)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = fmt.Sprintf("Wall time: %.4f seconds\nProcess timed out after %d ms", elapsed.Seconds(), timeout.Milliseconds())
	} else if errors.Is(ctx.Err(), context.Canceled) {
		status = fmt.Sprintf("Wall time: %.4f seconds\nProcess canceled", elapsed.Seconds())
	} else if runErr != nil && exitCode == -1 {
		status += "\nFailed to start command: " + runErr.Error()
	}

	formattedOutput := output.String()
	if formattedOutput == "" {
		return common.NewDefaultToolResult(status)
	}
	return common.NewDefaultToolResult(status + "\nFinal output:\n" + formattedOutput)
}

func stringInput(inputs map[string]any, name string) (string, bool) {
	value, ok := inputs[name]
	if !ok {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

func durationInput(
	inputs map[string]any,
	name string,
	defaultValue time.Duration,
	maximumValue time.Duration,
) (time.Duration, error) {
	value, ok := inputs[name]
	if !ok {
		return defaultValue, nil
	}

	milliseconds, err := numericInt64(value)
	if err != nil || milliseconds <= 0 {
		return 0, fmt.Errorf("%s parameter must be a positive integer", name)
	}
	if milliseconds > maximumValue.Milliseconds() {
		return maximumValue, nil
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func integerInput(inputs map[string]any, name string, defaultValue, minimumValue, maximumValue int) (int, error) {
	value, ok := inputs[name]
	if !ok {
		return defaultValue, nil
	}

	parsed, err := numericInt64(value)
	if err != nil || parsed < int64(minimumValue) {
		return 0, fmt.Errorf("%s parameter must be an integer greater than or equal to %d", name, minimumValue)
	}
	if parsed > int64(maximumValue) {
		return maximumValue, nil
	}
	return int(parsed), nil
}

func numericInt64(value any) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int8:
		return int64(number), nil
	case int16:
		return int64(number), nil
	case int32:
		return int64(number), nil
	case int64:
		return number, nil
	case uint:
		if uint64(number) > uint64(^uint64(0)>>1) {
			return 0, strconv.ErrRange
		}
		return int64(number), nil
	case uint8:
		return int64(number), nil
	case uint16:
		return int64(number), nil
	case uint32:
		return int64(number), nil
	case uint64:
		if number > uint64(^uint64(0)>>1) {
			return 0, strconv.ErrRange
		}
		return int64(number), nil
	case float32:
		return exactFloatInt64(float64(number))
	case float64:
		return exactFloatInt64(number)
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func exactFloatInt64(number float64) (int64, error) {
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
		number < math.MinInt64 || number >= math.MaxInt64 {
		return 0, fmt.Errorf("not an integer")
	}
	parsed := int64(number)
	return parsed, nil
}

type commandOutputBuffer struct {
	maximumBytes int
	head         []byte
	tail         []byte
	totalBytes   int64
}

func newCommandOutputBuffer(maximumBytes int) commandOutputBuffer {
	return commandOutputBuffer{
		maximumBytes: maximumBytes,
		head:         make([]byte, 0, maximumBytes/2),
		tail:         make([]byte, 0, maximumBytes-maximumBytes/2),
	}
}

func (b *commandOutputBuffer) Write(data []byte) (int, error) {
	written := len(data)
	b.totalBytes += int64(written)

	headCapacity := b.maximumBytes / 2
	if len(b.head) < headCapacity {
		headBytes := min(len(data), headCapacity-len(b.head))
		b.head = append(b.head, data[:headBytes]...)
		data = data[headBytes:]
	}

	tailCapacity := b.maximumBytes - headCapacity
	if len(data) >= tailCapacity {
		b.tail = append(b.tail[:0], data[len(data)-tailCapacity:]...)
		return written, nil
	}
	if overflow := len(b.tail) + len(data) - tailCapacity; overflow > 0 {
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:len(b.tail)-overflow]
	}
	b.tail = append(b.tail, data...)
	return written, nil
}

func (b *commandOutputBuffer) String() string {
	if b.totalBytes <= int64(b.maximumBytes) {
		return string(b.head) + string(b.tail)
	}

	truncatedBytes := b.totalBytes - int64(len(b.head)+len(b.tail))
	marker := fmt.Sprintf("\n... %d bytes truncated ...\n", truncatedBytes)
	available := b.maximumBytes - len(marker)
	if available <= truncationBoundaryBytes*2 {
		return string(b.head) + string(b.tail[:max(0, available-len(b.head))])
	}

	headBytes := available / 2
	tailBytes := available - headBytes
	return string(b.head[:min(headBytes, len(b.head))]) + marker + string(b.tail[len(b.tail)-min(tailBytes, len(b.tail)):])
}
