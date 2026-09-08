package backups

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
)

const MaxCommandOutput = 2 << 20

var ErrCommandFailed = errors.New("backup command failed")

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  map[string]string
}

type CommandRunner interface {
	Run(context.Context, Command) ([]byte, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, command Command) ([]byte, error) {
	if command.Name == "" {
		return nil, ErrCommandFailed
	}
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = command.Dir
	process.Env = commandEnvironment(command.Env)
	var output bytes.Buffer
	process.Stdout = &limitedWriter{writer: &output, remaining: MaxCommandOutput}
	process.Stderr = io.Discard
	if err := process.Run(); err != nil {
		return nil, ErrCommandFailed
	}
	return output.Bytes(), nil
}

func commandEnvironment(values map[string]string) []string {
	environment := []string{
		"HOME=/tmp",
		"LANG=C.UTF-8",
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=/tmp",
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key != "" && values[key] != "" {
			environment = append(environment, key+"="+values[key])
		}
	}
	return environment
}

type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (writer *limitedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if writer.remaining > 0 {
		chunk := value
		if len(chunk) > writer.remaining {
			chunk = chunk[:writer.remaining]
		}
		if _, err := writer.writer.Write(chunk); err != nil {
			return 0, err
		}
		writer.remaining -= len(chunk)
	}
	return original, nil
}

func readSecret(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(value)), nil
}
