package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"testing"

	log "github.com/sirupsen/logrus"
)

func TestDeniedLfsDownload(t *testing.T) {
	contentFilename := "b68143e6463773b1b6c6fd009a76c32aeec041faff32ba2ed42fd7f708a17f80"
	url := fmt.Sprintf("gitlab-lfs/objects/%s", contentFilename)

	prepareDownloadDir(t)
	deniedXSendfileDownload(t, contentFilename, url)
}

func TestAllowedLfsDownload(t *testing.T) {
	contentFilename := "b68143e6463773b1b6c6fd009a76c32aeec041faff32ba2ed42fd7f708a17f80"
	url := fmt.Sprintf("gitlab-lfs/objects/%s", contentFilename)

	prepareDownloadDir(t)
	allowedXSendfileDownload(t, contentFilename, url)
}

func allowedXSendfileDownload(t *testing.T, contentFilename string, filePath string) {
	contentPath := path.Join(cacheDir, contentFilename)
	prepareDownloadDir(t)

	// Prepare test server and backend
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("UPSTREAM", r.Method, r.URL)
		if xSendfileType := r.Header.Get("X-Sendfile-Type"); xSendfileType != "X-Sendfile" {
			t.Fatalf(`X-Sendfile-Type want "X-Sendfile" got %q`, xSendfileType)
		}
		w.Header().Set("X-Sendfile", contentPath)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, contentFilename))
		w.Header().Set("Content-Type", fmt.Sprintf(`application/octet-stream`))
		w.WriteHeader(200)
	}))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	contentBytes := []byte("content")
	if err := ioutil.WriteFile(contentPath, contentBytes, 0644); err != nil {
		t.Fatal(err)
	}

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s", ws.URL, filePath))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	actual, err := ioutil.ReadFile(path.Join(scratchDir, contentFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, contentBytes) {
		t.Fatal("Unexpected file contents in download")
	}
}

func deniedXSendfileDownload(t *testing.T, contentFilename string, filePath string) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("UPSTREAM", r.Method, r.URL)
		if xSendfileType := r.Header.Get("X-Sendfile-Type"); xSendfileType != "X-Sendfile" {
			t.Fatalf(`X-Sendfile-Type want "X-Sendfile" got %q`, xSendfileType)
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, contentFilename))
		w.WriteHeader(200)
		fmt.Fprint(w, "Denied")
	}))
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s", ws.URL, filePath))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	actual, err := ioutil.ReadFile(path.Join(scratchDir, contentFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, []byte("Denied")) {
		t.Fatal("Unexpected file contents in download")
	}
}
