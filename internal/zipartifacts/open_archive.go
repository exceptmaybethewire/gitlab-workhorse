package zipartifacts

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jfbus/httprs"
	opentracing "github.com/opentracing/opentracing-go"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// ErrNotAZip will be used when the file is not a zip archive
var ErrNotAZip = errors.New("not a zip")

// ErrNotAZip will be used when the file can't be found
var ErrArchiveNotFound = errors.New("archive not found")

var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 10 * time.Second,
		}).DialContext,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// OpenArchive will open a zip.Reader from a local path or a remote object store URL
// in case of remote url it will make use of ranged requestes to support seeking.
// If the path do not exists error will be ErrArchiveNotFound,
// if the file isn't a zip archive error will be ErrNotAZip
func OpenArchive(ctx context.Context, archivePath string) (*zip.Reader, error) {
	if isURL(archivePath) {
		return openHTTPArchive(ctx, archivePath)
	}

	return openFileArchive(ctx, archivePath)
}

func isURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

func openHTTPArchive(ctx context.Context, archivePath string) (*zip.Reader, error) {
	scrubbedArchivePath := helper.ScrubURLParams(archivePath)
	req, err := http.NewRequest(http.MethodGet, archivePath, nil)
	if err != nil {
		return nil, fmt.Errorf("Can't create HTTP GET %q: %v", scrubbedArchivePath, err)
	}

	if span := opentracing.SpanFromContext(ctx); span != nil {
		// Transmit the span's TraceContext as HTTP headers on our
		// outbound request.
		opentracing.GlobalTracer().Inject(
			span.Context(),
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(req.Header))
	}

	resp, err := httpClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %q: %v", scrubbedArchivePath, err)
	} else if resp.StatusCode == http.StatusNotFound {
		return nil, ErrArchiveNotFound
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET %q: %d: %v", scrubbedArchivePath, resp.StatusCode, resp.Status)
	}

	rs := httprs.NewHttpReadSeeker(resp, httpClient)

	go func() {
		<-ctx.Done()
		resp.Body.Close()
		rs.Close()
	}()

	archive, err := zip.NewReader(rs, resp.ContentLength)
	if err != nil {
		return nil, ErrNotAZip
	}

	return archive, nil
}

func openFileArchive(ctx context.Context, archivePath string) (*zip.Reader, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrArchiveNotFound
		}
		return nil, ErrNotAZip
	}

	go func() {
		<-ctx.Done()
		// We close the archive from this goroutine so that we can safely return a *zip.Reader instead of a *zip.ReadCloser
		archive.Close()
	}()

	return &archive.Reader, nil
}
