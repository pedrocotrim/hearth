package money

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"10", 1000},
		{"10.5", 1050},
		{"10.50", 1050},
		{"1,25", 125},
		{"1.234,56", 123456},
		{"1,234.56", 123456},
		{"$12.00", 1200},
		{"R$ 10,00", 1000},
		{"0", 0},
		{"0.00", 0},
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Parse(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "-1", "10.555", "abc", "1,2,3"} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) succeeded", in)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Format(1050, "USD"); got != "$10.50" {
		t.Fatalf("USD format: %s", got)
	}
	if got := Format(1000, "BRL"); got != "R$10.00" {
		t.Fatalf("BRL format: %s", got)
	}
	if got := Format(-50, "EUR"); got != "-€0.50" {
		t.Fatalf("negative format: %s", got)
	}
}
