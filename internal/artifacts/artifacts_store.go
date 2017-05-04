package artifacts

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"time"
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
			Help: "Describes how many requests is currently open in given state",
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
		Name: "gitlab_workhorse_object_storage_upload_time",
		Help: "How long it took to upload objects",
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
	defer objectStorageUploadTime.Observe(time.Since(started).Seconds())

	file, err := os.Open(fileName)
	if err != nil {
		objectStorageUploadRequestsFileFailed.Inc()
		return fmt.Errorf("%q open failed with: %v", fileName, err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		objectStorageUploadRequestsFileFailed.Inc()
		return fmt.Errorf("%q stat failed with: %v", fileName, err)
	}

	req, err := http.NewRequest("PUT", a.ObjectStore.StoreURL, file)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return fmt.Errorf("PUT %q failed with: %v", a.ObjectStore.StoreURL, err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	objectStorageUploadsOpen.Inc()
	defer objectStorageUploadsOpen.Dec()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		objectStorageUploadRequestsRequestFailed.Inc()
		return fmt.Errorf("request %q failed with: %v", a.ObjectStore.StoreURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		objectStorageUploadRequestsInvalidStatus.Inc()
		return fmt.Errorf("request %v failed with: %d %s", a.ObjectStore.StoreURL, resp.StatusCode, resp.Status)
	}

	writer.WriteField(formName+".store_url", a.ObjectStore.StoreURL)
	writer.WriteField(formName+".object_id", a.ObjectStore.ObjectID)

	objectStorageUploadRequestsSucceeded.Inc()
	objectStorageUploadBytes.Add(float64(fi.Size()))

	// Allow to upload only once using given credentials
	a.stored = true
	return nil
}
