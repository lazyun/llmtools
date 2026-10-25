package pi

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// npm generates a quoted %dp0% target followed by %*. Resolve that script
// directly rather than passing paths/model arguments through cmd.exe parsing.
// Only the standard Node shim shape is accepted; arbitrary batch files are not
// interpreted. This helper is portable so the parser can be tested on any host.
func npmNodeTarget(shim string) (string, error) {
	file, err := os.Open(shim)
	if err != nil {
		return "", err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return "", err
	}
	if len(body) > 65536 {
		return "", os.ErrInvalid
	}
	pattern := regexp.MustCompile(`(?im)"%dp0%[\\/]+([^"\r\n]+\.(?:cjs|mjs|js))"\s+%\*`)
	matches := pattern.FindAllSubmatch(body, -1)
	if len(matches) != 1 {
		return "", os.ErrInvalid
	}
	name := filepath.Clean(filepath.Join(filepath.Dir(shim), filepath.FromSlash(strings.ReplaceAll(string(matches[0][1]), "\\", "/"))))
	if info, err := os.Stat(name); err != nil || !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return name, nil
}
