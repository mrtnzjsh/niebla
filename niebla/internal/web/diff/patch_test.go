package diff

import "testing"

func TestApplyLineRangeReplacement(t *testing.T) {
	original := "line 1\nline 2\nline 3\nline 4\nline 5\n"

	got, err := ApplyLineRangeReplacement(original, 2, 3, "new line 2a\nnew line 2b")
	if err != nil {
		t.Fatalf("ApplyLineRangeReplacement failed: %v", err)
	}

	want := "line 1\nnew line 2a\nnew line 2b\nline 4\nline 5\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

