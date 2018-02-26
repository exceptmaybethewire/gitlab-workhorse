package test

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// ObjectstoreStub is a testing implementation of ObjectStore.
// Instead of storing objects it will just save md5sum.
type ObjectstoreStub struct {
	// bucket contains md5sum of uploaded objects
	bucket map[string]string
	// overwriteMD5 contains overwrites for md5sum that should be return instead of the regular hash
	overwriteMD5 map[string]string

	puts    int
	deletes int

	m sync.Mutex
}

// StartObjectStore will start an ObjectStore stub
func StartObjectStore() (*ObjectstoreStub, *httptest.Server) {
	return StartObjectStoreWithCustomMD5(make(map[string]string))
}

// StartObjectStoreWithCustomMD5 will start an ObjectStore stub: md5Hashes contains overwrites for md5sum that should be return on PutObject
func StartObjectStoreWithCustomMD5(md5Hashes map[string]string) (*ObjectstoreStub, *httptest.Server) {
	os := &ObjectstoreStub{
		bucket:       make(map[string]string),
		overwriteMD5: make(map[string]string),
	}

	for k, v := range md5Hashes {
		os.overwriteMD5[k] = v
	}

	return os, httptest.NewServer(os)
}

// PutsCnt counts PutObject invocations
func (o *ObjectstoreStub) PutsCnt() int {
	o.m.Lock()
	defer o.m.Unlock()

	return o.puts
}

// DeletesCnt counts DeleteObject invocation of a valid object
func (o *ObjectstoreStub) DeletesCnt() int {
	o.m.Lock()
	defer o.m.Unlock()

	return o.deletes
}

// GetObjectMD5 return the calculated MD5 of the object uploaded to path
// it will return an empty string if no object has been uploaded on such path
func (o *ObjectstoreStub) GetObjectMD5(path string) string {
	o.m.Lock()
	defer o.m.Unlock()

	return o.bucket[path]
}

func (o *ObjectstoreStub) removeObject(w http.ResponseWriter, r *http.Request) {
	o.m.Lock()
	defer o.m.Unlock()

	objectPath := r.URL.Path
	if _, ok := o.bucket[objectPath]; ok {
		o.deletes++
		delete(o.bucket, objectPath)

		w.WriteHeader(200)
	} else {
		w.WriteHeader(404)
	}
}

func (o *ObjectstoreStub) putObject(w http.ResponseWriter, r *http.Request) {
	o.m.Lock()
	defer o.m.Unlock()

	objectPath := r.URL.Path

	etag, overwritten := o.overwriteMD5[objectPath]
	if !overwritten {
		hasher := md5.New()
		io.Copy(hasher, r.Body)

		checksum := hasher.Sum(nil)
		etag = hex.EncodeToString(checksum)
	}

	o.puts++
	o.bucket[objectPath] = etag

	w.Header().Set("ETag", etag)
	w.WriteHeader(200)
}

func (o *ObjectstoreStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
	}

	fmt.Println("ObjectStore Stub:", r.Method, r.URL.Path)

	switch r.Method {
	case "DELETE":
		o.removeObject(w, r)
	case "PUT":
		o.putObject(w, r)
	default:
		w.WriteHeader(404)
	}
}
