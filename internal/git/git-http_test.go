package git

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
)

const (
	expectedBytes = 102400
	GL_ID         = "test-user"
)

// From https://npf.io/2015/06/testing-exec-command/
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestGitCommandProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	return cmd
}

func createTestPayload() []byte {
	return bytes.Repeat([]byte{'0'}, expectedBytes)
}

func TestHandleUploadPack(t *testing.T) {
	testHandlePostRpc(t, "git-upload-pack", handleUploadPack)
}

func TestHandleReceivePack(t *testing.T) {
	testHandlePostRpc(t, "git-receive-pack", handleReceivePack)
}

func testHandlePostRpc(t *testing.T, action string, handler func(*GitHttpResponseWriter, *http.Request, *api.Response) error) {
	defer func(oldTesting bool) {
		Testing = oldTesting
	}(Testing)
	Testing = true

	execCommand = fakeExecCommand
	defer func() { execCommand = exec.Command }()

	testInput := createTestPayload()
	body := bytes.NewReader([]byte(testInput))
	url := fmt.Sprintf("/gitlab/gitlab-ce.git/?service=%s", action)
	req, err := http.NewRequest("GET", url, body)

	if err != nil {
		t.Fatal(err)
	}

	resp := &api.Response{GL_ID: GL_ID}

	rr := httptest.NewRecorder()
	handler(NewGitHttpResponseWriter(rr), req, resp)

	// Check HTTP status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: expected: %v, got %v",
			http.StatusOK, status)
	}

	ct := fmt.Sprintf("application/x-%s-result", action)
	headers := []struct {
		key   string
		value string
	}{
		{"Content-Type", ct},
		{"Cache-Control", "no-cache"},
	}

	// Check HTTP headers
	for _, h := range headers {
		if value := rr.Header().Get(h.key); value != h.value {
			t.Errorf("HTTP header %v does not match: expected: %v, got %v",
				h.key, h.value, value)
		}
	}

	if rr.Body.String() != string(testInput) {
		t.Errorf("handler did not receive expected data: got %d, expected %d bytes",
			len(rr.Body.String()), len(testInput))
	}
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func TestGitCommandProcess(t *testing.T) {
	if os.Getenv("GL_ID") != GL_ID {
		return
	}

	defer os.Exit(0)

	uploadPack := stringInSlice("upload-pack", os.Args)

	if uploadPack {
		// First, send a large payload to stdout so that this executable will be blocked
		// until the reader consumes the data
		testInput := createTestPayload()
		body := bytes.NewReader([]byte(testInput))
		io.Copy(os.Stdout, body)

		// Now consume all the data to unblock the sender
		ioutil.ReadAll(os.Stdin)
	} else {
		io.Copy(os.Stdout, os.Stdin)
	}
}
