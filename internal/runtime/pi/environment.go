package pi

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// npm installs Pi beside its Node executable, while macOS GUI applications
// often inherit a PATH without that installation. Keep the configured shim's
// directory (not its symlink target) available to its shebang interpreter.
func commandEnvironment(binary string, overrides []string) []string {
	env := append(os.Environ(), overrides...)
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return env
	}
	dir, err := filepath.Abs(filepath.Dir(resolved))
	if err != nil {
		return env
	}
	path := ""
	for i := len(env) - 1; i >= 0; i-- {
		key, value, ok := strings.Cut(env[i], "=")
		if ok && (key == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(key, "PATH")) {
			path = value
			break
		}
	}
	for _, entry := range filepath.SplitList(path) {
		if entry == dir || runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			return env
		}
	}
	if path != "" {
		dir += string(os.PathListSeparator) + path
	}
	return append(env, "PATH="+dir)
}
