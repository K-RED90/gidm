package bridge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openPath opens path with the OS default handler. The path is passed as its own
// argv element (never interpolated into a shell string), so spaces or shell
// metacharacters in the filename cannot be misinterpreted.
func openPath(path string) error {
	if path == "" {
		return fmt.Errorf("bridge: open: empty path")
	}
	switch runtime.GOOS {
	case "darwin":
		return spawn("open", path)
	case "windows":
		// `start` is a cmd builtin; the empty "" is the (required) window-title arg.
		return spawn("cmd", "/c", "start", "", path)
	default: // linux, *bsd
		return spawn("xdg-open", path)
	}
}

// revealPath shows path in the OS file manager, selecting the file where the
// platform supports it. While a download is still in flight the final file does
// not exist yet (only its .part sibling), and "select this file" no-ops on a
// missing path — so we select the .part if present, else just open the containing
// directory. That way Reveal always lands somewhere useful.
func revealPath(path string) error {
	if path == "" {
		return fmt.Errorf("bridge: reveal: empty path")
	}
	target := path
	if !exists(target) {
		if part := path + ".part"; exists(part) {
			target = part
		} else {
			return openDir(filepath.Dir(path))
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return spawn("open", "-R", target)
	case "windows":
		// explorer wants the whole "/select,<path>" as a single argument.
		return spawn("explorer", "/select,"+target)
	default:
		// Most Linux file managers have no portable "select" verb; open the dir.
		return openDir(filepath.Dir(target))
	}
}

// openDir opens a directory in the OS file manager.
func openDir(dir string) error {
	switch runtime.GOOS {
	case "darwin":
		return spawn("open", dir)
	case "windows":
		return spawn("explorer", dir)
	default:
		return spawn("xdg-open", dir)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// spawn launches an opener without blocking the caller and reaps it in the
// background. The opener's exit code is ignored on purpose: some (notably Windows
// explorer) report non-zero even on success, and a successful launch is all we need.
func spawn(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("bridge: launch %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
