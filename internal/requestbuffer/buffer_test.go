package requestbuffer

import (
	"bytes"
	"io/ioutil"
	"testing"
)

func TestBuffer(t *testing.T) {
	cases := []string{
		"",
		"0",
		"01234",
		"0123456789",
	}
	for _, c := range cases {
		rb := &requestBuffer{dynamicBufferSize: 5}
		result, err := rb.buffer(ioutil.NopCloser(bytes.NewReader([]byte(c))))
		if err != nil {
			t.Fatalf("case %q: %v", c, err)
		}
		value, err := ioutil.ReadAll(result)
		if err != nil {
			panic(err)
		}
		if string(value) != c {
			t.Fatalf("expected %q, received %q", c, value)
		}
	}
}
