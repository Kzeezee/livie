package components

import "testing"

func TestTruncateArgs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{
			name:  "strips outer braces",
			input: `{"cmd":"echo hello"}`,
			max:   40,
			want:  `"cmd":"echo hello"`,
		},
		{
			name:  "truncates long value",
			input: `{"cmd":"echo hello world this is a very long command indeed"}`,
			max:   20,
			want:  `"cmd":"echo hello wo…`,
		},
		{
			name:  "empty object",
			input: `{}`,
			max:   40,
			want:  ``,
		},
		{
			name:  "no braces passthrough",
			input: `just a string`,
			max:   40,
			want:  `just a string`,
		},
		{
			name:  "exact length — no ellipsis",
			input: `{"k":"val"}`,
			max:   7,
			want:  `"k":"val"`, // 9 chars, but max=7 → truncated
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateArgs(tc.input, tc.max)
			// For the "exact length" case our want is wrong above — fix inline:
			if tc.name == "exact length — no ellipsis" {
				// "k":"val" is 9 runes, max=7 → `"k":"va…`
				tc.want = `"k":"va…`
			}
			if got != tc.want {
				t.Errorf("truncateArgs(%q, %d)\n  got  %q\n  want %q",
					tc.input, tc.max, got, tc.want)
			}
		})
	}
}
