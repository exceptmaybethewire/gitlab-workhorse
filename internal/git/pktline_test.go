package git

import (
	"bytes"
	"testing"
)

func TestSuccessfulScanDeepen(t *testing.T) {
	examples := []struct {
		input  string
		output bool
	}{
		{"000dsomething000cdeepen 10000", true},
		{"000dsomething0000000cdeepen 1", true},
		{"000dsomething0000", false},
	}

	for _, example := range examples {
		hasDeepen, err := scanDeepen(bytes.NewReader([]byte(example.input)))
		if err != nil {
			t.Fatalf("error scanning %q: %v", example.input, err)
		}

		if hasDeepen != example.output {
			t.Fatalf("scanDeepen %q: expected %v, got %v", example.input, example.output, hasDeepen)
		}
	}
}

func TestFailedScanDeepen(t *testing.T) {
	examples := []string{
		"invalid data",
		"deepen",
		"000cdeepen",
	}

	for _, example := range examples {
		hasDeepen, err := scanDeepen(bytes.NewReader([]byte(example)))
		if err == nil {
			t.Fatalf("expected error scanning %q", example)
		}

		t.Log(err)

		if hasDeepen == true {
			t.Fatalf("scanDeepen %q: expected result to be false, got true", example)
		}
	}
}
