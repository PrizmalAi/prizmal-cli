package opencode

import (
	"strings"
	"testing"
)

// OpenCode 2.x drops a malformed OPENCODE_CONFIG_CONTENT document with only a
// logged warning, so the launcher must validate it and fail loudly instead,
// rather than hand a 2.x install a document it will silently discard.
func TestEnsureValidConfigContentRejectsMalformedPrecomputedContent(t *testing.T) {
	o := &OpenCode{configContent: "{not json"}
	if err := o.ensureValidConfigContent("wire-test-model", wireKeyTestModel()); err == nil {
		t.Fatal("ensureValidConfigContent accepted malformed JSON")
	}
}

// A normal launch, with a model to resolve, builds and caches valid JSON and
// returns no error.
func TestEnsureValidConfigContentAcceptsAWellFormedBuild(t *testing.T) {
	o := &OpenCode{}
	if err := o.ensureValidConfigContent("wire-test-model", wireKeyTestModel()); err != nil {
		t.Fatalf("ensureValidConfigContent: %v", err)
	}
	if o.configContent == "" {
		t.Fatal("ensureValidConfigContent built no content")
	}
	if !strings.Contains(o.configContent, "wire-test-model") {
		t.Fatalf("cached content %q does not name the launch model", o.configContent)
	}
}

// With no model to resolve, there is nothing to validate: this is today's
// existing no-model path, not a validation failure.
func TestEnsureValidConfigContentIsANoopWithNoModel(t *testing.T) {
	o := &OpenCode{}
	if err := o.ensureValidConfigContent("", nil); err != nil {
		t.Fatalf("ensureValidConfigContent with no model: %v", err)
	}
	if o.configContent != "" {
		t.Fatalf("configContent = %q, want empty with no model to resolve", o.configContent)
	}
}
