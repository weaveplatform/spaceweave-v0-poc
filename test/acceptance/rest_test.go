package acceptance

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func control(
	t *testing.T,
	endpoint, product, operator string,
	args ...string,
) ([]byte, string, int) {
	t.Helper()
	cmd := exec.Command(controlBinary, append([]string{"--endpoint", endpoint}, args...)...)
	cmd.Env = append(
		os.Environ(),
		"GOCOVERDIR="+coverDir,
		"SPACEWEAVECTL_TOKEN="+product,
		"SPACEWEAVECTL_OPERATOR_TOKEN="+operator,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return stdout.Bytes(), stderr.String(), code
}
