package diagnostic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRequestIDIsRandomAndValid(t *testing.T) {
	first, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRequestID(first) || !ValidRequestID(second) || first == second {
		t.Fatalf("request IDs = %q, %q", first, second)
	}
}

func TestEncodeResultEnforcesPayloadLimits(t *testing.T) {
	requestID, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	checks := make([]Check, 200)
	for index := range checks {
		checks[index] = Check{
			Code: "xray.config", Status: StatusFail,
			Label: strings.Repeat("名", 200), Endpoint: strings.Repeat("e", 400),
			Detail: strings.Repeat("detail", 300),
		}
	}
	payload, err := EncodeResult(Result{
		RequestID: requestID, StartedAt: 1, DurationMS: 10, Checks: checks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > MaxResultBytes {
		t.Fatalf("payload size = %d", len(payload))
	}
	var result Result
	if err := json.Unmarshal(payload, &result); err != nil || ValidateResult(result) != nil {
		t.Fatalf("encoded result invalid: %v, %+v", err, result)
	}
	if len(result.Checks) > MaxChecks || result.Checks[len(result.Checks)-1].Code != "diagnostic.truncated" {
		t.Fatalf("limited checks = %d, last = %+v", len(result.Checks), result.Checks[len(result.Checks)-1])
	}
	for _, check := range result.Checks {
		if len(check.Detail) > MaxDetailBytes || len(check.Label) > MaxLabelBytes || len(check.Endpoint) > MaxEndpointBytes {
			t.Fatalf("unbounded check = %+v", check)
		}
	}
}

func TestValidateResultRejectsUncleanText(t *testing.T) {
	requestID, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	result := Result{
		Type: "diagnostic_result", RequestID: requestID, StartedAt: 1,
		Checks: []Check{{Code: "xray.config", Status: StatusFail, Detail: "unsafe\ntext"}},
	}
	if !errors.Is(ValidateResult(result), ErrInvalidResult) {
		t.Fatal("diagnostic result with control characters was accepted")
	}
}
