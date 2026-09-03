package validation

import "testing"

func TestAllPassed(t *testing.T) {
	if !AllPassed([]Result{{Passed: true}, {Passed: true}}) {
		t.Fatal("all successful results should pass")
	}
	if AllPassed([]Result{{Passed: true}, {Passed: false}}) {
		t.Fatal("a failed result must fail validation")
	}
}
