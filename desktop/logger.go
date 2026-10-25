package main

import (
	"fmt"
	"io"
)

// Wails trace messages can contain binding arguments/results (including an
// explicitly revealed credential). Never send framework payloads to a logger.
type safeRuntimeLogger struct{ output io.Writer }

func (l safeRuntimeLogger) Print(string) {}
func (l safeRuntimeLogger) Trace(string) {}
func (l safeRuntimeLogger) Debug(string) {}
func (l safeRuntimeLogger) Info(string)  {}
func (l safeRuntimeLogger) Warning(string) {
	fmt.Fprintln(l.output, "DESKTOP_RUNTIME_WARNING: check application state")
}
func (l safeRuntimeLogger) Error(string) {
	fmt.Fprintln(l.output, "DESKTOP_RUNTIME_ERROR: operation failed")
}
func (l safeRuntimeLogger) Fatal(string) {
	fmt.Fprintln(l.output, "DESKTOP_RUNTIME_ERROR: fatal runtime failure")
}
