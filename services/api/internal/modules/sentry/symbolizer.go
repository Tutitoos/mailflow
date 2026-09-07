package sentry

import (
	"archive/zip"
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/json"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

type artifactSymbolizer func(*normalizedFrame) bool

type sourceMapDocument struct {
	Version  int      `json:"version"`
	File     string   `json:"file"`
	Sources  []string `json:"sources"`
	Names    []string `json:"names"`
	Mappings string   `json:"mappings"`
}

type sourceMapEntry struct {
	generatedLine   int64
	generatedColumn int64
	source          int
	originalLine    int64
	originalColumn  int64
	name            int
}

type nativeSymbol struct {
	address uint64
	name    string
}

func newArtifactSymbolizer(kind, name string, content []byte) (artifactSymbolizer, error) {
	switch kind {
	case "sourcemap":
		return newSourceMapSymbolizer(name, content)
	case "dsym", "dif":
		return newNativeSymbolizer(content)
	default:
		return nil, ErrInvalidArtifact
	}
}

func newSourceMapSymbolizer(artifactName string, content []byte) (artifactSymbolizer, error) {
	var document sourceMapDocument
	if json.Unmarshal(content, &document) != nil || document.Version != 3 || document.Mappings == "" {
		return nil, ErrInvalidArtifact
	}
	entries, err := decodeSourceMapMappings(document.Mappings)
	if err != nil || len(entries) == 0 {
		return nil, ErrInvalidArtifact
	}
	generatedFile := path.Base(strings.ReplaceAll(document.File, "\\", "/"))
	if generatedFile == "." || generatedFile == "" {
		generatedFile = strings.TrimSuffix(path.Base(artifactName), ".map")
	}
	return func(frame *normalizedFrame) bool {
		if frame.Line <= 0 || (frame.Filename != "" && generatedFile != "" && frame.Filename != generatedFile) {
			return false
		}
		line, column := frame.Line-1, frame.Column
		if column > 0 {
			column--
		}
		index := sort.Search(len(entries), func(index int) bool {
			entry := entries[index]
			return entry.generatedLine > line || entry.generatedLine == line && entry.generatedColumn > column
		}) - 1
		if index < 0 || entries[index].generatedLine != line {
			return false
		}
		entry := entries[index]
		if entry.source < 0 || entry.source >= len(document.Sources) {
			return false
		}
		source := path.Base(strings.ReplaceAll(document.Sources[entry.source], "\\", "/"))
		if !safeFilenamePattern.MatchString(source) {
			return false
		}
		frame.Filename = source
		frame.Line = entry.originalLine + 1
		frame.Column = entry.originalColumn + 1
		if entry.name >= 0 && entry.name < len(document.Names) {
			if function := safeSymbol(document.Names[entry.name]); function != "" {
				frame.Function = function
			}
		}
		return true
	}, nil
}

func decodeSourceMapMappings(mappings string) ([]sourceMapEntry, error) {
	entries := make([]sourceMapEntry, 0)
	source, originalLine, originalColumn, name := 0, int64(0), int64(0), 0
	for lineIndex, line := range strings.Split(mappings, ";") {
		generatedColumn := int64(0)
		if line == "" {
			continue
		}
		for _, segment := range strings.Split(line, ",") {
			values, err := decodeVLQSegment(segment)
			if err != nil || len(values) != 1 && len(values) != 4 && len(values) != 5 {
				return nil, ErrInvalidArtifact
			}
			generatedColumn += int64(values[0])
			if generatedColumn < 0 || len(values) == 1 {
				continue
			}
			source += values[1]
			originalLine += int64(values[2])
			originalColumn += int64(values[3])
			entry := sourceMapEntry{generatedLine: int64(lineIndex), generatedColumn: generatedColumn, source: source, originalLine: originalLine, originalColumn: originalColumn, name: -1}
			if len(values) == 5 {
				name += values[4]
				entry.name = name
			}
			if source < 0 || originalLine < 0 || originalColumn < 0 || name < 0 {
				return nil, ErrInvalidArtifact
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func decodeVLQSegment(segment string) ([]int, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	values := make([]int, 0, 5)
	value, shift := 0, 0
	for _, encoded := range segment {
		digit := strings.IndexRune(alphabet, encoded)
		if digit < 0 || shift > 30 {
			return nil, ErrInvalidArtifact
		}
		value |= (digit & 31) << shift
		if digit&32 != 0 {
			shift += 5
			continue
		}
		negative := value&1 == 1
		decoded := value >> 1
		if negative {
			decoded = -decoded
		}
		values = append(values, decoded)
		value, shift = 0, 0
	}
	if shift != 0 || len(values) == 0 {
		return nil, ErrInvalidArtifact
	}
	return values, nil
}

func newNativeSymbolizer(content []byte) (artifactSymbolizer, error) {
	symbols := nativeSymbols(content)
	if len(symbols) == 0 {
		return nil, ErrInvalidArtifact
	}
	sort.Slice(symbols, func(left, right int) bool { return symbols[left].address < symbols[right].address })
	return func(frame *normalizedFrame) bool {
		address, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(frame.Instruction), "0x"), 16, 64)
		if err != nil {
			return false
		}
		index := sort.Search(len(symbols), func(index int) bool { return symbols[index].address > address }) - 1
		if index < 0 {
			return false
		}
		if address-symbols[index].address > 16<<20 {
			return false
		}
		function := safeSymbol(strings.TrimPrefix(symbols[index].name, "_"))
		if function == "" {
			return false
		}
		frame.Function = function
		return true
	}, nil
}

func nativeSymbols(content []byte) []nativeSymbol {
	if symbols := machoSymbols(content); len(symbols) > 0 {
		return symbols
	}
	if symbols := elfSymbols(content); len(symbols) > 0 {
		return symbols
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil
	}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || entry.UncompressedSize64 > MaxArtifactBytes {
			continue
		}
		file, openErr := entry.Open()
		if openErr != nil {
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, MaxArtifactBytes+1))
		_ = file.Close()
		if readErr == nil && len(payload) <= MaxArtifactBytes {
			if symbols := machoSymbols(payload); len(symbols) > 0 {
				return symbols
			}
			if symbols := elfSymbols(payload); len(symbols) > 0 {
				return symbols
			}
		}
	}
	return nil
}

func machoSymbols(content []byte) []nativeSymbol {
	file, err := macho.NewFile(bytes.NewReader(content))
	if err != nil {
		fat, fatErr := macho.NewFatFile(bytes.NewReader(content))
		if fatErr != nil {
			return nil
		}
		defer fat.Close()
		for _, architecture := range fat.Arches {
			if symbols := machoFileSymbols(architecture.File); len(symbols) > 0 {
				return symbols
			}
		}
		return nil
	}
	defer file.Close()
	return machoFileSymbols(file)
}

func machoFileSymbols(file *macho.File) []nativeSymbol {
	if file.Symtab == nil {
		return nil
	}
	symbols := make([]nativeSymbol, 0, len(file.Symtab.Syms))
	for _, symbol := range file.Symtab.Syms {
		if symbol.Value > 0 && symbol.Name != "" {
			symbols = append(symbols, nativeSymbol{address: symbol.Value, name: symbol.Name})
		}
	}
	return symbols
}

func elfSymbols(content []byte) []nativeSymbol {
	file, err := elf.NewFile(bytes.NewReader(content))
	if err != nil {
		return nil
	}
	defer file.Close()
	regular, _ := file.Symbols()
	dynamic, _ := file.DynamicSymbols()
	all := append(regular, dynamic...)
	symbols := make([]nativeSymbol, 0, len(all))
	for _, symbol := range all {
		if symbol.Value > 0 && symbol.Name != "" {
			symbols = append(symbols, nativeSymbol{address: symbol.Value, name: symbol.Name})
		}
	}
	return symbols
}
