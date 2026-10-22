package launch

import (
	"slices"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
		wantErr  bool
	}{
		{"empty", "", nil, false},
		{"blank", "  \t ", nil, false},
		{"words", "--a  --b value", []string{"--a", "--b", "value"}, false},
		{"single quotes", `--append 'two words'`, []string{"--append", "two words"}, false},
		{"double quotes", `--append "two words"`, []string{"--append", "two words"}, false},
		{"escaped quote", `--x "say \"hi\""`, []string{"--x", `say "hi"`}, false},
		{"empty quoted word", `--x ""`, []string{"--x", ""}, false},
		{"windows path", `--add-dir C:\work\src`, []string{"--add-dir", `C:\work\src`}, false},
		{"unterminated", `--x "open`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitArgs(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeMissingFlags(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		configured []string
		want       []string
	}{
		{"adds an absent flag", []string{"-p", "hi"}, []string{"--effort", "high"}, []string{"-p", "hi", "--effort", "high"}},
		{"skips a present flag and its value", []string{"--effort", "low"}, []string{"--effort", "high", "--verbose"}, []string{"--effort", "low", "--verbose"}},
		{"skips a flag present as name=value", []string{"--effort=low"}, []string{"--effort", "high"}, []string{"--effort=low"}},
		{"matches a configured name=value", []string{"--effort", "low"}, []string{"--effort=high"}, []string{"--effort", "low"}},
		{"keeps a repeated configured flag", nil, []string{"--add-dir", "a", "--add-dir", "b"}, []string{"--add-dir", "a", "--add-dir", "b"}},
		{"keeps words before the first flag", nil, []string{"word", "--x"}, []string{"word", "--x"}},
		{"a lone dash is a value", nil, []string{"--x", "-"}, []string{"--x", "-"}},
		{"no configured arguments", []string{"--a"}, nil, []string{"--a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.args)
			got := MergeMissingFlags(tc.args, tc.configured)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !slices.Equal(tc.args, before) {
				t.Fatalf("args mutated: %q", tc.args)
			}
		})
	}
}
