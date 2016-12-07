package looseblob

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

type Handler struct {
	rawFile    *os.File
	zlibReader io.ReadCloser
	*blobPath
}

func NewHandler(repoPath, blobId string) (h *Handler, err error) {
	blobPath, err := newBlobPath(repoPath, blobId)
	if err != nil {
		return nil, err
	}

	h = &Handler{blobPath: blobPath}
	defer func() {
		if err != nil {
			h.Close()
		}
	}()

	if h.rawFile, err = os.Open(h.Path()); err != nil {
		return h, err
	}

	if h.zlibReader, err = zlib.NewReader(h.rawFile); err != nil {
		return h, err
	}

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("looseBlobOject: sending %q for %q", h.Path(), r.URL.Path)

	bufReader := bufio.NewReader(h.zlibReader)
	objectHeader, err := bufReader.ReadString(0)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("looseBlobOject: read blob header: %v", err))
		return
	}

	objectHeader = strings.TrimSuffix(objectHeader, "\x00")
	prefix := "blob "
	if !strings.HasPrefix(objectHeader, prefix) {
		helper.LogError(r, fmt.Errorf("looseBlobOject: invalid object header: %q", objectHeader))
		return
	}
	w.Header().Set("Content-Length", strings.TrimPrefix(objectHeader, prefix))

	if _, err := io.Copy(w, bufReader); err != nil {
		helper.LogError(r, fmt.Errorf("looseBlobOject: copy loose blob: %v", err))
		return
	}
}

func (h *Handler) Close() error {
	if h.zlibReader != nil {
		h.zlibReader.Close()
	}

	if h.rawFile != nil {
		return h.rawFile.Close()
	}

	return nil
}
