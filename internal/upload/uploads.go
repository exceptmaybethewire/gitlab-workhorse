package upload

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// These methods are allowed to have thread-unsafe implementations.
type MultipartFormProcessor interface {
	ProcessFile(ctx context.Context, formName, fileName string, writer *multipart.Writer) error
	ProcessField(ctx context.Context, formName string, writer *multipart.Writer) error
	Finalize(ctx context.Context) error
	Name() string
}

func HandleFileUploads(w http.ResponseWriter, r *http.Request, h http.Handler, tempPath string, filter MultipartFormProcessor) {
	if tempPath == "" {
		helper.Fail500(w, r, fmt.Errorf("handleFileUploads: tempPath empty"))
		return
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	defer writer.Close()

	// Rewrite multipart form data
	err := rewriteFormFilesFromMultipart(r, writer, tempPath, filter)
	if err != nil {
		if err == http.ErrNotMultipart {
			h.ServeHTTP(w, r)
		} else {
			helper.Fail500(w, r, fmt.Errorf("handleFileUploads: extract files from multipart: %v", err))
		}
		return
	}

	// Close writer
	writer.Close()

	// Hijack the request
	r.Body = ioutil.NopCloser(&body)
	r.ContentLength = int64(body.Len())
	r.Header.Set("Content-Type", writer.FormDataContentType())

	if err := filter.Finalize(r.Context()); err != nil {
		helper.Fail500(w, r, fmt.Errorf("handleFileUploads: Finalize: %v", err))
		return
	}

	// Proxy the request
	h.ServeHTTP(w, r)
}
