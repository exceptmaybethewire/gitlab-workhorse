package upload

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
)

var (
	multipartUploadRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{

			Name: "gitlab_workhorse_multipart_upload_requests",
			Help: "How many multipart upload requests have been processed by gitlab-workhorse. Partitioned by type.",
		},
		[]string{"type"},
	)

	multipartFileUploadBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_multipart_upload_bytes",
			Help: "How many disk bytes of multipart file parts have been succesfully written by gitlab-workhorse. Partitioned by type.",
		},
		[]string{"type"},
	)

	multipartFiles = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_multipart_upload_files",
			Help: "How many multipart file parts have been processed by gitlab-workhorse. Partitioned by type.",
		},
		[]string{"type"},
	)
)

type rewriter struct {
	writer  *multipart.Writer
	preauth *api.Response
	filter  MultipartFormProcessor
}

func init() {
	prometheus.MustRegister(multipartUploadRequests)
	prometheus.MustRegister(multipartFileUploadBytes)
	prometheus.MustRegister(multipartFiles)
}

func rewriteFormFilesFromMultipart(r *http.Request, writer *multipart.Writer, preauth *api.Response, filter MultipartFormProcessor) error {
	// Create multipart reader
	reader, err := r.MultipartReader()
	if err != nil {
		if err == http.ErrNotMultipart {
			// We want to be able to recognize http.ErrNotMultipart elsewhere so no fmt.Errorf
			return http.ErrNotMultipart
		}
		return fmt.Errorf("get multipart reader: %v", err)
	}

	multipartUploadRequests.WithLabelValues(filter.Name()).Inc()

	rew := &rewriter{
		writer:  writer,
		preauth: preauth,
		filter:  filter,
	}

	for {
		p, err := reader.NextPart()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		name := p.FormName()
		if name == "" {
			continue
		}

		// Copy form field
		if p.FileName() != "" {
			err = rew.handleFilePart(r.Context(), name, p)
		} else {
			err = rew.copyPart(r.Context(), name, p)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (rew *rewriter) handleFilePart(ctx context.Context, name string, p *multipart.Part) error {
	multipartFiles.WithLabelValues(rew.filter.Name()).Inc()

	filename := p.FileName()

	if strings.Contains(filename, "/") || filename == "." || filename == ".." {
		return fmt.Errorf("illegal filename: %q", filename)
	}

	opts := filestore.GetOpts(rew.preauth)
	opts.TempFilePrefix = filename

	fh, err := filestore.SaveFileFromReader(ctx, p, -1, opts)
	if err != nil {
		if err == filestore.ErrEntityTooLarge {
			return err
		}
		return fmt.Errorf("Persisting multipart file: %v", err)
	}

	for key, value := range fh.GitLabFinalizeFields(name) {
		rew.writer.WriteField(key, value)
	}

	multipartFileUploadBytes.WithLabelValues(rew.filter.Name()).Add(float64(fh.Size))

	return rew.filter.ProcessFile(ctx, name, fh, rew.writer)
}

func (rew *rewriter) copyPart(ctx context.Context, name string, p *multipart.Part) error {
	np, err := rew.writer.CreatePart(p.Header)
	if err != nil {
		return fmt.Errorf("create multipart field: %v", err)
	}

	if _, err := io.Copy(np, p); err != nil {
		return fmt.Errorf("duplicate multipart field: %v", err)
	}

	if err := rew.filter.ProcessField(ctx, name, rew.writer); err != nil {
		return fmt.Errorf("process multipart field: %v", err)
	}

	return nil
}
