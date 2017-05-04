package artifacts

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/net/context/ctxhttp"

	"github.com/prometheus/client_golang/prometheus"
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
	prometheus.MustRegister(
		objectStorageUploadRequests,
		objectStorageUploadsOpen,
		objectStorageUploadBytes)
}

func (a *artifactsUploadProcessor) storeFile(formName, fileName string, writer *multipart.Writer) error {
	if a.ObjectStore.StoreURL == "" {
		return nil
	}

	if a.stored {
		objectStorageUploadRequestsMultipleUploads.Inc()
		return nil
	}

	started := time.Now()
	defer func() {
		objectStorageUploadTime.Observe(time.Since(started).Seconds())
	}()

	file, err := os.Open(fileName)
	if err != nil {
		objectStorageUploadRequestsFileFailed.Inc()
		return err
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		objectStorageUploadRequestsFileFailed.Inc()
		return err
	}

	req, err := http.NewRequest("PUT", a.ObjectStore.StoreURL, file)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return fmt.Errorf("PUT %q: %v", a.ObjectStore.StoreURL, err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	objectStorageUploadsOpen.Inc()
	defer objectStorageUploadsOpen.Dec()

	timeout := DefaultObjectStoreTimeoutSeconds
	if a.ObjectStore.Timeout != 0 {
		timeout = a.ObjectStore.Timeout
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancelFn()

	resp, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return fmt.Errorf("PUT request %q: %v", a.ObjectStore.StoreURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		objectStorageUploadRequestsInvalidStatus.Inc()
		return fmt.Errorf("PUT request %v returned: %d %s", a.ObjectStore.StoreURL, resp.StatusCode, resp.Status)
	}

	writer.WriteField(formName+".store_url", a.ObjectStore.StoreURL)
	writer.WriteField(formName+".object_id", a.ObjectStore.ObjectID)

	objectStorageUploadRequestsSucceeded.Inc()
	objectStorageUploadBytes.Add(float64(fi.Size()))

	// Allow to upload only once using given credentials
	a.stored = true
	return nil
}
