package looseblob

import (
	"testing"
)

func TestNewBlobPath(t *testing.T) {
	testCases := []struct {
		blobId string
		ok     bool
	}{
		{"69f4dccb6121848aea6313d4049f3916ec575824", true},
		{"69f4dccb6121848aea63/13d40493916ec575824", false},
		{"hello world!!", false},
		{"69f4dccb612", false},
	}

	for _, tc := range testCases {
		_, err := newBlobPath("/tmp", tc.blobId)

		if err == nil && !tc.ok {
			t.Errorf("Expected %q to be invalid but got no error", tc.blobId)
		}

		if err != nil && tc.ok {
			t.Errorf("Expected %q to be valid but got an error: %v", tc.blobId, err)
		}
	}
}
