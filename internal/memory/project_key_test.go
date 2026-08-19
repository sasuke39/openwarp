package memory

import "testing"

func TestProjectKeyFromRoot(t *testing.T) {
	got := ProjectKeyFromRoot("/Users/a/mywarp/local-adapter")
	if got == "" || got == "unknown" {
		t.Errorf("expected valid key, got %q", got)
	}
	// Should match SanitizePathKey output.
	expected := SanitizePathKey("/Users/a/mywarp/local-adapter")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestProjectKeyFromRoot_Empty(t *testing.T) {
	got := ProjectKeyFromRoot("")
	if got != "unknown" {
		t.Errorf("expected 'unknown', got %q", got)
	}
}

func TestShortID_Long(t *testing.T) {
	got := ShortID("abcdefghijklmnop")
	if got != "abcdefgh" {
		t.Errorf("expected 'abcdefgh', got %q", got)
	}
}

func TestShortID_Short(t *testing.T) {
	got := ShortID("abc")
	if got != "abc" {
		t.Errorf("expected 'abc', got %q", got)
	}
}

func TestShortID_Empty(t *testing.T) {
	got := ShortID("")
	if got != "unknown" {
		t.Errorf("expected 'unknown', got %q", got)
	}
}

func TestProjectKeyFromRoot_SameWorkspace(t *testing.T) {
	k1 := ProjectKeyFromRoot("/Users/a/mywarp")
	k2 := ProjectKeyFromRoot("/Users/a/mywarp")
	if k1 != k2 {
		t.Errorf("same workspace should produce same key: %q vs %q", k1, k2)
	}
}

func TestProjectKeyFromRoot_DifferentWorkspace(t *testing.T) {
	k1 := ProjectKeyFromRoot("/Users/a/project1")
	k2 := ProjectKeyFromRoot("/Users/a/project2")
	if k1 == k2 {
		t.Errorf("different workspaces should produce different keys: both %q", k1)
	}
}
