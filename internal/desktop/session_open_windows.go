//go:build windows

package desktop

// O_NONBLOCK does not exist on Windows; the regular-file Stat check in
// matchingPiSession already rejects pipes and other special files that could
// block a header read.
func nonBlockFlag() int { return 0 }
