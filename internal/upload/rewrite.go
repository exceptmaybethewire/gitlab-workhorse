package upload

import (
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
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
	writer      *multipart.Writer
	filter      MultipartFormProcessor
	directories []string
	config      FileUploadsConfig
}

func init() {
	prometheus.MustRegister(multipartUploadRequests)
	prometheus.MustRegister(multipartFileUploadBytes)
	prometheus.MustRegister(multipartFiles)
}

func rewriteFormFilesFromMultipart(r *http.Request, writer *multipart.Writer, config FileUploadsConfig, filter MultipartFormProcessor) (cleanup func(), err error) {
	// Create multipart reader
	reader, err := r.MultipartReader()
	if err != nil {
		if err == http.ErrNotMultipart {
			// We want to be able to recognize http.ErrNotMultipart elsewhere so no fmt.Errorf
			return nil, http.ErrNotMultipart
		}
		return nil, fmt.Errorf("get multipart reader: %v", err)
	}

	multipartUploadRequests.WithLabelValues(filter.Name()).Inc()

	rew := &rewriter{
		writer:   writer,
		filter:   filter,
		config:   config,
	}

	cleanup = func() {
		for _, dir := range rew.directories {
			os.RemoveAll(dir)
		}
	}

	// Execute cleanup in case of failure
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	for {
		p, err := reader.NextPart()
		if err != nil {
			if err == io.EOF {
				break
			}
			return cleanup, err
		}

		name := p.FormName()
		if name == "" {
			continue
		}

		// Copy form field
		if p.FileName() != "" {
			err = rew.handleFilePart(name, p)

		} else {
			err = rew.copyPart(name, p)
		}

		if err != nil {
			return cleanup, err
		}
	}

	return cleanup, nil
}

func (rew *rewriter) uploadFile(name string, file io.Writer, part *multipart.Part) (int64, error) {
	if rew.fileUploaded {
		return 0, fmt.Errorf("rewriteFormFilesFromMultipart: only single file can be uploaded: %v")
	}
	rew.fileUploaded = true

	pr, pw := io.Pipe()
	defer pr.Close()

	writer := io.MultiWriter(file, pw)

	var written int64
	go func() {
		// copy data to output
		var err error
		defer pw.CloseWithError(err)
		written, err = io.Copy(writer, part)
	}()

	rew.writer.WriteField(name+".upload_path", rew.config.UploadPath)

	println(part.Header)

	req, err := http.NewRequest("PUT", rew.config.UploadURL, pr)
	if err != nil {
		return 0, err
	}
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/octet-stream")
	//req.ContentLength, err = strconv.ParseInt(part.Header.Get("Content-Length"), 10, 0)
	//if err != nil {
	//	return 0, err
	//}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("uploadFile: upload file to: %v failed with: %v", rew.config.UploadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("uploadFile: upload file to: %v failed with: %d %s", rew.config.UploadURL, resp.StatusCode, resp.Status)
	}

	return written, nil
}

func (rew *rewriter) handleFilePart(name string, p *multipart.Part) error {
	multipartFiles.WithLabelValues(rew.filter.Name()).Inc()

	filename := p.FileName()

	if strings.Contains(filename, "/") || filename == "." || filename == ".." {
		return fmt.Errorf("illegal filename: %q", filename)
	}

	// Create temporary directory where the uploaded file will be stored
	if err := os.MkdirAll(rew.config.TempPath, 0700); err != nil {
		return fmt.Errorf("mkdir for tempfile: %v", err)
	}

	tempDir, err := ioutil.TempDir(rew.config.TempPath, "multipart-")
	if err != nil {
		return fmt.Errorf("create tempdir: %v", err)
	}
	rew.directories = append(rew.directories, tempDir)

	file, err := os.OpenFile(path.Join(tempDir, filename), os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("rewriteFormFilesFromMultipart: temp file: %v", err)
	}
	defer file.Close()

	// Add file entry
	rew.writer.WriteField(name+".path", file.Name())
	rew.writer.WriteField(name+".name", filename)

	var written int64
	if rew.config.UploadURL != "" {
		written, err = rew.uploadFile(name, file, p)
	} else {
		written, err = io.Copy(file, p)
	}

	if err != nil {
		return fmt.Errorf("copy from multipart to tempfile: %v", err)
	}
	multipartFileUploadBytes.WithLabelValues(rew.filter.Name()).Add(float64(written))

	file.Close()

	if err := rew.filter.ProcessFile(name, file.Name(), rew.writer); err != nil {
		return err
	}

	return nil
}

func (rew *rewriter) copyPart(name string, p *multipart.Part) error {
	np, err := rew.writer.CreatePart(p.Header)
	if err != nil {
		return fmt.Errorf("create multipart field: %v", err)
	}

	if _, err := io.Copy(np, p); err != nil {
		return fmt.Errorf("duplicate multipart field: %v", err)
	}

	if err := rew.filter.ProcessField(name, rew.writer); err != nil {
		return fmt.Errorf("process multipart field: %v", err)
	}

	return nil
}
