package sentry

import (
	"os"
	"strconv"
	"testing"
)

func TestSourceMapSymbolizerResolvesOriginalFrameWithoutPrivatePaths(t *testing.T) {
	payload := []byte(`{"version":3,"file":"bundle.js","sources":["/Users/private/src/app.ts"],"sourcesContent":["private source"],"names":["renderInbox"],"mappings":"AAAAA"}`)
	normalized, err := normalizeSourceMap(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(normalized) == string(payload) || containsBytes(normalized, []byte("/Users/private")) || containsBytes(normalized, []byte("private source")) {
		t.Fatalf("source map was not redacted: %s", normalized)
	}
	symbolize, err := newArtifactSymbolizer("sourcemap", "bundle.js.map", normalized)
	if err != nil {
		t.Fatal(err)
	}
	frame := normalizedFrame{Filename: "bundle.js", Line: 1, Column: 1}
	if !symbolize(&frame) || frame.Filename != "app.ts" || frame.Function != "renderInbox" || frame.Line != 1 || frame.Column != 1 {
		t.Fatalf("frame was not resolved: %+v", frame)
	}
}

func TestNativeSymbolizerReadsBuildSymbols(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	symbols := nativeSymbols(payload)
	if len(symbols) == 0 {
		t.Skip("test binary does not expose a native symbol table")
	}
	symbolize, err := newNativeSymbolizer(payload)
	if err != nil {
		t.Fatal(err)
	}
	frame := normalizedFrame{Instruction: "0x" + strconv.FormatUint(symbols[0].address, 16)}
	if !symbolize(&frame) || frame.Function == "" {
		t.Fatalf("native address was not resolved: %+v", frame)
	}
}

func containsBytes(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		match := true
		for offset := range fragment {
			if value[index+offset] != fragment[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
