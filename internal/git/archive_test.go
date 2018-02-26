package git

import (
	"io/ioutil"
	"net/http/httptest"
	"testing"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func TestParseBasename(t *testing.T) {
	for _, testCase := range []struct {
		in  string
		out pb.GetArchiveRequest_Format
	}{
		{"archive", pb.GetArchiveRequest_TAR_GZ},
		{"master.tar.gz", pb.GetArchiveRequest_TAR_GZ},
		{"foo-master.tgz", pb.GetArchiveRequest_TAR_GZ},
		{"foo-v1.2.1.gz", pb.GetArchiveRequest_TAR_GZ},
		{"foo.tar.bz2", pb.GetArchiveRequest_TAR_BZ2},
		{"archive.tbz", pb.GetArchiveRequest_TAR_BZ2},
		{"archive.tbz2", pb.GetArchiveRequest_TAR_BZ2},
		{"archive.tb2", pb.GetArchiveRequest_TAR_BZ2},
		{"archive.bz2", pb.GetArchiveRequest_TAR_BZ2},
	} {
		basename := testCase.in
		out, ok := parseBasename(basename)
		if !ok {
			t.Fatalf("parseBasename did not recognize %q", basename)
		}

		if out != testCase.out {
			t.Fatalf("expected %q, got %q", testCase.out, out)
		}
	}
}

func TestFinalizeArchive(t *testing.T) {
	tempFile, err := ioutil.TempFile("", "gitlab-workhorse-test")
	if err != nil {
		t.Fatal(err)
	}
	defer tempFile.Close()

	// Deliberately cause an EEXIST error: we know tempFile.Name() already exists
	err = finalizeCachedArchive(tempFile, tempFile.Name())
	if err != nil {
		t.Fatalf("expected nil from finalizeCachedArchive, received %v", err)
	}
}

func TestSetArchiveHeaders(t *testing.T) {
	for _, testCase := range []struct {
		in  pb.GetArchiveRequest_Format
		out string
	}{
		{pb.GetArchiveRequest_ZIP, "application/zip"},
		{pb.GetArchiveRequest_TAR, "application/octet-stream"},
		{pb.GetArchiveRequest_TAR_GZ, "application/octet-stream"},
		{pb.GetArchiveRequest_TAR_BZ2, "application/octet-stream"},
	} {
		w := httptest.NewRecorder()

		// These should be replaced, not appended to
		w.Header().Set("Content-Type", "test")
		w.Header().Set("Content-Length", "test")
		w.Header().Set("Content-Disposition", "test")
		w.Header().Set("Cache-Control", "test")

		setArchiveHeaders(w, testCase.in, "filename")

		testhelper.AssertResponseWriterHeader(t, w, "Content-Type", testCase.out)
		testhelper.AssertResponseWriterHeader(t, w, "Content-Length")
		testhelper.AssertResponseWriterHeader(t, w, "Content-Disposition", `attachment; filename="filename"`)
		testhelper.AssertResponseWriterHeader(t, w, "Cache-Control", "private")
	}
}
