package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// /dev/null is a character device, so a file-mode check calls it a terminal.
// That made 'vllm-helper logs > /dev/null' follow forever instead of printing
// and exiting, and it left sudo prompting where nothing could answer.
func TestDevNullIsNotATerminal(t *testing.T) {
	t.Parallel()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	if isTerminal(devNull) {
		t.Error("/dev/null reports as a terminal, so logs would tail forever and sudo would prompt")
	}
}

func TestRegularFilesAndPipesAreNotTerminals(t *testing.T) {
	t.Parallel()

	reg, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	if isTerminal(reg) {
		t.Error("a regular file reports as a terminal")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if isTerminal(r) || isTerminal(w) {
		t.Error("a pipe reports as a terminal")
	}
}

// The negative cases above would all pass against a function that just
// returned false, so prove a real terminal still reads as one. CI has no
// controlling terminal, hence the skip; `script -q /dev/null go test` runs it.
func TestARealTerminalIsDetected(t *testing.T) {
	t.Parallel()

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no controlling terminal:", err)
	}
	defer tty.Close()

	if !isTerminal(tty) {
		t.Error("/dev/tty does not report as a terminal")
	}
}

// isTerminal is what the tests above exercise, so pin that each caller reads
// the descriptor it names. Crossing the two is the one mistake a wrapper this
// small can still make.
func TestTheTTYHelpersReadTheirOwnDescriptor(t *testing.T) {
	t.Parallel()

	if got, want := stdoutIsTTY(), isTerminal(os.Stdout); got != want {
		t.Errorf("stdoutIsTTY() = %v, want %v -- it is not reading os.Stdout", got, want)
	}
	if got, want := stdinIsTTY(), isTerminal(os.Stdin); got != want {
		t.Errorf("stdinIsTTY() = %v, want %v -- it is not reading os.Stdin", got, want)
	}
}
