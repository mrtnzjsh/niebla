package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"citc-toolchain/pkg/events"
)

// ExecConfig specifies settings for the command execution sink driver.
type ExecConfig struct {
	Command string
	Timeout time.Duration
}

// ExecSink pipes JSON-serialized events to an external binary/script on standard input.
type ExecSink struct {
	command string
	timeout time.Duration
}

// NewExecSink creates a new ExecSink instance.
func NewExecSink(cfg ExecConfig) (*ExecSink, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("command path cannot be empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &ExecSink{
		command: cfg.Command,
		timeout: timeout,
	}, nil
}

func (s *ExecSink) Name() string {
	return "exec"
}

// Send serializes the event to JSON and executes the configured command with payload on stdin.
func (s *ExecSink) Send(ctx context.Context, evt events.Event) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event %s: %w", evt.ID, err)
	}

	execCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, s.command)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(cmd.Environ(),
		fmt.Sprintf("CITC_EVENT_TYPE=%s", evt.Type),
		fmt.Sprintf("CITC_EVENT_ID=%s", evt.ID),
		fmt.Sprintf("CITC_EVENT_SUBJECT=%s", evt.Subject),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exec sink command %q failed: %w (output: %s)", s.command, err, string(out))
	}

	return nil
}

func (s *ExecSink) Close() error {
	return nil
}
