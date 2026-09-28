package subscription

import "testing"

func TestFormatNodeDisplayName(t *testing.T) {
	tests := []struct {
		name string
		bp   int
		want string
	}{
		{"新加坡 联通移动", 50, "新加坡 联通移动 [0.5×]"},
		{"美国 三网优化", 100, "美国 三网优化 [1×]"},
		{"日本 联通", 125, "日本 联通 [1.25×]"},
		{"日本 联通", 200, "日本 联通 [2×]"},
		{"德国 三网优化", 80, "德国 三网优化 [0.8×]"},
		{"香港", 500, "香港 [5×]"},
	}
	for _, test := range tests {
		if got := FormatNodeDisplayName(test.name, test.bp); got != test.want {
			t.Errorf("FormatNodeDisplayName(%q, %d) = %q, want %q", test.name, test.bp, got, test.want)
		}
	}
}
