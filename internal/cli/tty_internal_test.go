package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// withStdout swaps os.Stdout for the duration of a test. The helpers read the
// process's real descriptors, which is the thing under test, so there is
// nothing to inject.
func withStdout(t *testing.T, f *os.File) {
	t.Helper()
	saved := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = saved })
}

func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved })
}

// /dev/null is a character device, so a file-mode check calls it a terminal.
// That made 'vllm-helper logs > /dev/null' follow forever instead of printing
// and exiting, and it left sudo prompting where nothing could answer.
func TestDevNullIsNotATerminal(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	withStdout(t, devNull)
	if stdoutIsTTY() {
		t.Error("stdout redirected to /dev/null reports as a terminal, so logs would tail forever")
	}
	withStdin(t, devNull)
	if stdinIsTTY() {
		t.Error("stdin from /dev/null reports as a terminal, so prompts would wait on nothing")
	}
}

func TestRegularFilesAndPipesAreNotTerminals(t *testing.T) {
	reg, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	withStdout(t, reg)
	if stdoutIsTTY() {
		t.Error("a regular file reports as a terminal")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	withStdout(t, w)
	if stdoutIsTTY() {
		t.Error("a pipe reports as a terminal")
	}
	withStdin(t, r)
	if stdinIsTTY() {
		t.Error("a pipe reports as a terminal on stdin")
	}
}

// The negative cases above would all pass against a function that always
// returned false, so prove a real terminal still reads as one. CI has no
// controlling terminal, hence the skip.
func TestARealTerminalIsDetected(t *testing.T) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no controlling terminal:", err)
	}
	defer tty.Close()

	withStdout(t, tty)
	if !stdoutIsTTY() {
		t.Error("/dev/tty does not report as a terminal on stdout")
	}
	withStdin(t, tty)
	if !stdinIsTTY() {
		t.Error("/dev/tty does not report as a terminal on stdin")
	}
}
