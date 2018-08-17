package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
)

func TestAllowedDownloadZip(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.zip", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTar(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "xf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTarGz(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar.gz"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar.gz", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "zxf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedDownloadTarBz2(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.tar.bz2"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/%s/repository/archive.tar.bz2", ws.URL, testProject))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("tar", "jxf", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedApiDownloadZip(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestAllowedApiDownloadZipWithSlash(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	// Use foo%2Fbar instead of a numeric ID
	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/foo%%2Fbar/repository/archive.zip", ws.URL))
	if !strings.Contains(downloadCmd.Args[3], `projects/foo%2Fbar/repository`) {
		t.Fatalf("Cannot find percent-2F: %v", downloadCmd.Args)
	}
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	extractCmd := exec.Command("unzip", archiveName)
	extractCmd.Dir = scratchDir
	runOrFail(t, extractCmd)
}

func TestDownloadCacheHit(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	cachedContent := []byte("cached")
	if err := ioutil.WriteFile(path.Join(cacheDir, archiveName), cachedContent, 0644); err != nil {
		t.Fatal(err)
	}

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	actual, err := ioutil.ReadFile(path.Join(scratchDir, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, cachedContent) {
		t.Fatal("Unexpected file contents in download")
	}
}

func TestDownloadCacheCreate(t *testing.T) {
	prepareDownloadDir(t)

	// Prepare test server and backend
	archiveName := "foobar.zip"
	ts := archiveOKServer(t, archiveName)
	defer ts.Close()
	ws := startWorkhorseServer(ts.URL)
	defer ws.Close()

	downloadCmd := exec.Command("curl", "-J", "-O", fmt.Sprintf("%s/api/v3/projects/123/repository/archive.zip", ws.URL))
	downloadCmd.Dir = scratchDir
	runOrFail(t, downloadCmd)

	compareCmd := exec.Command("cmp", path.Join(cacheDir, archiveName), path.Join(scratchDir, archiveName))
	if err := compareCmd.Run(); err != nil {
		t.Fatalf("Comparison between downloaded file and cache item failed: %s", err)
	}
}
