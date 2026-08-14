package memedge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dop251/goja"
	"github.com/tetratelabs/wazero"
)

// ValidationResult contains the details of a static code check.
type ValidationResult struct {
	Valid       bool        `json:"valid"`
	Runtime     RuntimeType `json:"runtime"`
	Entrypoint  string      `json:"entrypoint,omitempty"`
	ByteSize    int         `json:"byte_size"`
	Errors      []string    `json:"errors,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
}

// ValidateCode performs static analysis on JavaScript or WebAssembly code before storage or execution.
func ValidateCode(ctx context.Context, path string, code []byte, runtimeType RuntimeType) (*ValidationResult, error) {
	if len(code) == 0 {
		return &ValidationResult{
			Valid:    false,
			ByteSize: 0,
			Errors:   []string{"code payload is empty"},
		}, errors.New("empty code payload")
	}

	if runtimeType == "" || runtimeType == RuntimeAuto {
		runtimeType = DetectRuntime(path, code)
	}

	result := &ValidationResult{
		Runtime:  runtimeType,
		ByteSize: len(code),
		Errors:   []string{},
		Warnings: []string{},
	}

	switch runtimeType {
	case RuntimeWasm:
		validateWasm(ctx, code, result)
	case RuntimeJS:
		validateJS(code, result)
	default:
		validateJS(code, result)
	}

	result.Valid = len(result.Errors) == 0
	if !result.Valid {
		return result, fmt.Errorf("validation failed: %s", strings.Join(result.Errors, "; "))
	}

	return result, nil
}

func validateJS(code []byte, result *ValidationResult) {
	source := normalizeJSSource(string(code))
	
	// 1. Compile check using Goja AST parser
	_, err := goja.Compile("validation.js", source, true)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("JavaScript syntax error: %v", err))
		return
	}

	// 2. Check for entry point structure
	hasHandler := strings.Contains(source, "function handler") ||
		strings.Contains(source, "module.exports") ||
		strings.Contains(source, "export default")

	if !hasHandler {
		result.Warnings = append(result.Warnings, "No explicit handler or module.exports found; top-level expression will be executed")
	} else {
		result.Entrypoint = "handler"
	}
}

func validateWasm(ctx context.Context, code []byte, result *ValidationResult) {
	// 1. Check WebAssembly magic header (\x00asm)
	if len(code) < 4 || !bytes.Equal(code[:4], WasmMagicHeader) {
		result.Errors = append(result.Errors, "invalid WebAssembly binary: missing '\\x00asm' magic header")
		return
	}

	// 2. Check compilation and WASI structure with Wazero
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	_, err := r.CompileModule(ctx, code)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("WebAssembly compile error: %v", err))
		return
	}

	result.Entrypoint = "_start / main"
}
