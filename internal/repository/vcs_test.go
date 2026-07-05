package repository

import "testing"

func TestParseVCS(t *testing.T) {
	tests := []struct {
		input string
		want  VCS
		ok    bool
	}{
		{"auto", Auto, true},
		{"git", Git, true},
		{"jj", JJ, true},
		{"banana", "", false},
	}

	for _, test := range tests {
		got, err := ParseVCS(test.input)

		if test.ok {
			if err != nil {
				t.Fatalf("%q: %v", test.input, err)
			}

			if got != test.want {
				t.Fatalf(
					"%q: got %q want %q",
					test.input,
					got,
					test.want,
				)
			}
		} else if err == nil {
			t.Fatalf(
				"%q: expected error",
				test.input,
			)
		}
	}
}
