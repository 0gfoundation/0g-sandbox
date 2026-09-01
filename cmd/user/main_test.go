package main

import "testing"

func TestParse0GAmountToNeuronIsExact(t *testing.T) {
	tests := map[string]string{
		"0.2":        "200000000000000000",
		"0.01":       "10000000000000000",
		"1":          "1000000000000000000",
		"1.00000001": "1000000010000000000",
	}

	for input, want := range tests {
		got, err := parse0GAmountToNeuron(input)
		if err != nil {
			t.Fatalf("parse0GAmountToNeuron(%q) returned error: %v", input, err)
		}
		if got.String() != want {
			t.Fatalf("parse0GAmountToNeuron(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestParse0GAmountToNeuronRejectsInvalidAmounts(t *testing.T) {
	tests := []string{"", "abc", "-0.1", "0", "0.0000000000000000001"}

	for _, input := range tests {
		if got, err := parse0GAmountToNeuron(input); err == nil {
			t.Fatalf("parse0GAmountToNeuron(%q) = %s, want error", input, got)
		}
	}
}
