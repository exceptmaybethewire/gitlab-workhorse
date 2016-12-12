package looseblob

import (
	"os"
	"testing"
)

const testDir = "testdata"

func TestWriterFinalize(t *testing.T) {
	// ruby -e 'print "blob 10\x001234567890"' | shasum -
	// 6a537b5b367880eac21e3c0f0a382de7a19bd30a  -
	blobId := "6a537b5b367880eac21e3c0f0a382de7a19bd30a"
	blobPath := testDir + "/objects/6a/537b5b367880eac21e3c0f0a382de7a19bd30a"

	testCases := []struct {
		input string
		ok    bool
		desc  string
	}{
		{"1234567890", true, "success"},
		{"123456789", false, "short write"},
		{"12345678901", false, "long write"},
		{"abcdefghij", false, "checksum error"},
	}

	for _, tc := range testCases {
		os.Remove(blobPath)
		b, err := NewWriter(testDir, blobId, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Write([]byte(tc.input)); err != nil {
			t.Fatal(err)
		}
		err = b.Finalize()
		if tc.ok && err != nil {
			t.Errorf("Case %q: Expected Finalize() success, got %v", tc.desc, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("Case %q: Expected Finalize() failure but got no error", tc.desc)
		}

		_, err = os.Stat(blobPath)
		if tc.ok && err != nil {
			t.Errorf("Expected file %q to exist, got %v", blobPath, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("Expected file %q to not exist but got not stat error", blobPath)
		}
	}
}
