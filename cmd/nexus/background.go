package nexus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// startBackground re-execs the current process without the --background flag,
// redirecting stdout and stderr to ~/.config/nexus/logs/<logFile>.log.
// The child is placed in its own session (Setsid) so it survives terminal close.
// label is shown to the user, e.g. "Running nexus ingest".
func startBackground(label, logFile string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Rebuild args without --background so the child does not recurse.
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		if a == "--background" {
			continue
		}
		args = append(args, a)
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "nexus", "logs")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, logFile+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	bgCmd := exec.Command(exe, args...) //nolint:gosec
	bgCmd.Stdout = f
	bgCmd.Stderr = f
	bgCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := bgCmd.Start(); err != nil {
		_ = f.Close()
		return fmt.Errorf("start background process: %w", err)
	}
	_ = f.Close()

	fmt.Printf("\n  ⟳ %s in background [pid %d]\n", label, bgCmd.Process.Pid)
	fmt.Printf("    Log: %s\n", logPath)
	fmt.Printf("    tail -f %s\n\n", logPath)
	return nil
}
