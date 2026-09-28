package api

import (
	"encoding/json"
	"testing"
)

func TestDecodeTrafficMultiplier(t *testing.T) {
	for input, want := range map[string]int{
		"0.1":  10,
		"0.5":  50,
		"1":    100,
		"1.25": 125,
		"5":    500,
	} {
		got, set, err := decodeTrafficMultiplier(json.RawMessage(input))
		if err != nil || !set || got != want {
			t.Fatalf("decode multiplier %s = %d/%v, error = %v; want %d/true", input, got, set, err, want)
		}
	}
	for _, input := range []string{"0", "0.09", "5.01", "-1", "1.234", "null", `"1"`} {
		if _, _, err := decodeTrafficMultiplier(json.RawMessage(input)); err == nil {
			t.Fatalf("decode invalid multiplier %s succeeded", input)
		}
	}
	if got, set, err := decodeTrafficMultiplier(nil); err != nil || set || got != 0 {
		t.Fatalf("decode missing multiplier = %d/%v, error = %v", got, set, err)
	}
}
