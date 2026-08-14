package memedge

import (
	"context"
	"strings"
	"testing"
)

func TestValidateCode_ValidJS(t *testing.T) {
	ctx := context.Background()
	jsCode := `
export default function handler(req) {
	return { status: 200, body: "valid" };
}
`
	res, err := ValidateCode(ctx, "test.js", []byte(jsCode), RuntimeJS)
	if err != nil {
		t.Fatalf("expected valid JS, got error: %v", err)
	}
	if !res.Valid {
		t.Errorf("expected res.Valid to be true")
	}
	if res.Runtime != RuntimeJS {
		t.Errorf("expected RuntimeJS, got %v", res.Runtime)
	}
}

func TestValidateCode_InvalidJS(t *testing.T) {
	ctx := context.Background()
	brokenJS := `
function handler(req { // missing closing paren
	return 123;
`
	res, err := ValidateCode(ctx, "broken.js", []byte(brokenJS), RuntimeJS)
	if err == nil {
		t.Fatalf("expected validation error for broken JS, but got nil")
	}
	if res.Valid {
		t.Errorf("expected res.Valid to be false")
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected errors to be populated")
	}
}

func TestValidateCode_InvalidWasm(t *testing.T) {
	ctx := context.Background()
	fakeWasm := []byte("this is not a wasm binary")
	res, err := ValidateCode(ctx, "func.wasm", fakeWasm, RuntimeWasm)
	if err == nil {
		t.Fatalf("expected validation error for fake wasm")
	}
	if res.Valid {
		t.Errorf("expected res.Valid to be false")
	}
	if !strings.Contains(res.Errors[0], "missing '\\x00asm'") {
		t.Errorf("expected magic header error, got %v", res.Errors)
	}
}
