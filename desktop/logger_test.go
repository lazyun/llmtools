package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFrameworkLogsNeverContainBindingSecrets(t *testing.T) {
	var out bytes.Buffer
	l := safeRuntimeLogger{output: &out}
	for _, log := range []func(string){l.Print, l.Trace, l.Debug, l.Info, l.Warning, l.Error, l.Fatal} {
		log("Authorization: Bearer private-key; binding result = private-key")
	}
	if strings.Contains(out.String(), "private-key") || strings.Contains(out.String(), "Authorization") {
		t.Fatal("framework leaked secret payload")
	}
}
