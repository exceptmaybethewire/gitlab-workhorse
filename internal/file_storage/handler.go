package file_storage

import (
	"context"
	"fmt"
	"net/http"
	"io"
	"os"
	"time"
	"io/ioutil"

	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
)

var (
	DefaultObjectStoreTimeoutSeconds = 360
)

var (
	objectStorageUploadRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_object_storage_upload_requests",
			Help: "How many object storage requests have been processed",
		},
		[]string{"status"},
	)
	objectStorageUploadsOpen = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitlab_workhorse_object_storage_upload_open",
			Help: "Describes many object storage requests are open now",
		},
	)
	objectStorageUploadBytes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_object_storage_upload_bytes",
			Help: "How many bytes were sent to object storage",
		},
	)
	objectStorageUploadTime = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gitlab_workhorse_object_storage_upload_time",
			Help:    "How long it took to upload objects",
			Buckets: objectStorageUploadTimeBuckets,
		})

	objectStorageUploadRequestsFileFailed      = objectStorageUploadRequests.WithLabelValues("file-failed")
	objectStorageUploadRequestsRequestFailed   = objectStorageUploadRequests.WithLabelValues("request-failed")
	objectStorageUploadRequestsInvalidStatus   = objectStorageUploadRequests.WithLabelValues("invalid-status")
	objectStorageUploadRequestsSucceeded       = objectStorageUploadRequests.WithLabelValues("succeeded")
	objectStorageUploadRequestsMultipleUploads = objectStorageUploadRequests.WithLabelValues("multiple-uploads")

	objectStorageUploadTimeBuckets = []float64{.1, .25, .5, 1, 2.5, 5, 10, 25, 50, 100}
)

func init() {
	// prometheus.MustRegister(
	// 	objectStorageUploadRequests,
	// 	objectStorageUploadsOpen,
	// 	objectStorageUploadBytes)
}

type FileHandler struct {
	LocalPath string

	RemoteID string
	RemoteURL string

	Size int64
	Hashes map[string]string
}

func (fh *FileHandler) uploadRemoteFile(ctx context.Context, apiResponse *api.Response, size int64) (io.WriteCloser, func(), error) {
	pr, pw := io.Pipe()

	req, err := http.NewRequest("PUT", apiResponse.ObjectStore.StoreURL, pr)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return nil, nil, fmt.Errorf("PUT %q: %v", apiResponse.ObjectStore.StoreURL, err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size

	timeout := DefaultObjectStoreTimeoutSeconds
	if apiResponse.ObjectStore.Timeout != 0 {
		timeout = apiResponse.ObjectStore.Timeout
	}

	ctx2, ctxCancelFn := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	objectStorageUploadsOpen.Inc()

	fh.RemoteID = apiResponse.ObjectStore.ObjectID
	fh.RemoteURL = apiResponse.ObjectStore.GetURL

	finished := make(chan struct{})

	cancelFn := func() {
		ctxCancelFn()

		<- finished

		fmt.Println("Executing DELETE request against", apiResponse.ObjectStore.DeleteURL)

		req, err := http.NewRequest("DELETE", apiResponse.ObjectStore.DeleteURL, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				objectStorageUploadRequestsRequestFailed.Inc()
				fmt.Println(fmt.Errorf(req.Method, "request %q: %v", apiResponse.ObjectStore.DeleteURL, err))
				return
			}
			defer resp.Body.Close()
			fmt.Println(req.Method, "request", apiResponse.ObjectStore.DeleteURL, ":", resp.StatusCode, resp.Status)
		}
	}

	go func() {
		defer ctxCancelFn()
		defer objectStorageUploadsOpen.Dec()
		defer pr.Close()
		defer close(finished)

		req = req.WithContext(ctx2)

		fmt.Println("Sending to REMOTE storage...")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			objectStorageUploadRequestsRequestFailed.Inc()
			pr.CloseWithError(fmt.Errorf("PUT request %q: %v", apiResponse.ObjectStore.StoreURL, err))
			return
		}
		defer resp.Body.Close()

		data, err := ioutil.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			fmt.Println("Failed with REMOTE storage:", resp.StatusCode, resp.Status)
			fmt.Println(string(data))
			objectStorageUploadRequestsInvalidStatus.Inc()
			pr.CloseWithError(fmt.Errorf("PUT request %v returned: %d %s", apiResponse.ObjectStore.StoreURL, resp.StatusCode, resp.Status))
			return
		}

		fmt.Println("Done with REMOTE storage.")
	}()

	return pw, cancelFn, nil
}

func (fh *FileHandler) uploadLocalFile(ctx context.Context, apiResponse *api.Response, size int64) (io.WriteCloser, func(), error) {
	file, err := ioutil.TempFile(apiResponse.TempPath, "upload")
	if err != nil {
		return nil, nil, fmt.Errorf("handleStoreLfsObject: create tempfile: %v", err)
	}

	cancelFn := func() {
		os.Remove(file.Name())
	}

	fh.LocalPath = file.Name()
	return file, cancelFn, nil
}

func SaveFile(ctx context.Context, apiResponse *api.Response, reader io.Reader, size int64) (fh *FileHandler, cancelFn func(), err error) {
	var writers []io.Writer
	var cancelFns []func()

	fmt.Println("SaveFile", apiResponse)

	hashes := newMultiHash()
	writers = append(writers, hashes.Writer)

	fh = &FileHandler{}

	mainCancelFn := func() {
		for _, cancelFn := range cancelFns {
			cancelFn()
		}
	}

	if apiResponse.ObjectStore.StoreURL != "" {
		writer, cancelFn, err := fh.uploadRemoteFile(ctx, apiResponse, size)
		if err != nil {
			mainCancelFn()
			return nil, nil, err
		}

		writers = append(writers, writer)
		cancelFns = append(cancelFns, cancelFn)

		defer func() {
			err2 := writer.Close()
			if err2 != nil {
				fmt.Println("Failed writer:", err2)
				err = err2
			}
		}()
	}

	if apiResponse.TempPath != "" {
		writer, cancelFn, err := fh.uploadLocalFile(ctx, apiResponse, size)
		if err != nil {
			mainCancelFn()
			return nil, nil, err
		}

		writers = append(writers, writer)
		cancelFns = append(cancelFns, cancelFn)

		defer func() {
			err2 := writer.Close()
			if err2 != nil {
				fmt.Println("Failed writer:", err2)
				err = err2
			}
		}()
	}

	defer func() {
		if err != nil {
			mainCancelFn()
		}
	}()

	multiWriter := io.MultiWriter(writers...)
	fh.Size, err = io.Copy(multiWriter, reader)
	fh.Hashes = hashes.finish()
	return fh, mainCancelFn, err
}
