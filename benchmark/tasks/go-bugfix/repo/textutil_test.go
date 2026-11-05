package textutil

import "testing"

func TestReverse(t *testing.T) {
	if got, want := Reverse("hello"), "olleh"; got != want {
		t.Errorf("Reverse(hello) = %q, want %q", got, want)
	}
	if got, want := Reverse("héllo"), "olléh"; got != want {
		t.Errorf("Reverse(héllo) = %q, want %q", got, want)
	}
}

func TestTitle(t *testing.T) {
	cases := map[string]string{
		"hello world":     "Hello World",
		"hELLO wORLD":     "Hello World",
		"  padded  text ": "Padded Text",
		"élan vital":      "Élan Vital",
	}
	for in, want := range cases {
		if got := Title(in); got != want {
			t.Errorf("Title(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got, want := Truncate("abcdef", 3), "abc..."; got != want {
		t.Errorf("Truncate ascii = %q, want %q", got, want)
	}
	if got, want := Truncate("héllo", 2), "hé..."; got != want {
		t.Errorf("Truncate runes = %q, want %q", got, want)
	}
	if got, want := Truncate("abc", 0), "..."; got != want {
		t.Errorf("Truncate zero = %q, want %q", got, want)
	}
	if got, want := Truncate("abc", 5), "abc"; got != want {
		t.Errorf("Truncate passthrough = %q, want %q", got, want)
	}
}
