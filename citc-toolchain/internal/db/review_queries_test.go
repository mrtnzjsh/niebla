package db

import (
	"testing"
)

func TestCommentLineRangeStr(t *testing.T) {
	single := Comment{
		LineNumber:    42,
		EndLineNumber: 42,
	}
	if single.LineRangeStr() != "Line 42" {
		t.Errorf("expected 'Line 42', got %q", single.LineRangeStr())
	}
	if single.IsMultiLine() {
		t.Errorf("expected single line to return false for IsMultiLine")
	}

	multi := Comment{
		LineNumber:    42,
		EndLineNumber: 48,
	}
	if multi.LineRangeStr() != "Lines 42–48" {
		t.Errorf("expected 'Lines 42–48', got %q", multi.LineRangeStr())
	}
	if !multi.IsMultiLine() {
		t.Errorf("expected multi-line to return true for IsMultiLine")
	}

	// Implicit single line when EndLineNumber is 0
	implicit := Comment{
		LineNumber:    10,
		EndLineNumber: 0,
	}
	if implicit.LineRangeStr() != "Line 10" {
		t.Errorf("expected 'Line 10', got %q", implicit.LineRangeStr())
	}
}
