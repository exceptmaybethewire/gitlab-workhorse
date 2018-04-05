package test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sync"
)

// ObjectstoreStub is a testing implementation of ObjectStore.
type ObjectstoreStub struct {
	// bucket contains md5sum of uploaded objects
	bucket map[string]string
	// overwriteMD5 contains overwrites for md5sum that should be return instead of the regular hash
	overwriteMD5 map[string]string
	// storage is the folder containing uploaded files
	storage string

	puts    int
	deletes int

	m sync.Mutex
}

// StartObjectStore will start an ObjectStore stub
func StartObjectStore(ctx context.Context) (*ObjectstoreStub, *httptest.Server, error) {
	return StartObjectStoreWithCustomMD5(ctx, make(map[string]string))
}

// StartObjectStoreWithCustomMD5 will start an ObjectStore stub: md5Hashes contains overwrites for md5sum that should be return on PutObject
func StartObjectStoreWithCustomMD5(ctx context.Context, md5Hashes map[string]string) (*ObjectstoreStub, *httptest.Server, error) {
	dir, err := ioutil.TempDir("", "objectstore")
	if err != nil {
		return nil, nil, err
	}

	o := &ObjectstoreStub{
		bucket:       make(map[string]string),
		overwriteMD5: make(map[string]string),
		storage:      dir,
	}

	for k, v := range md5Hashes {
		o.overwriteMD5[k] = v
	}

	server := httptest.NewServer(o)

	go func() {
		<-ctx.Done()
		server.Close()
		os.RemoveAll(dir)
	}()

	return o, server, nil
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
		os.Remove(path.Join(o.storage, objectPath))

		w.WriteHeader(200)
	} else {
		w.WriteHeader(404)
	}
}

func (o *ObjectstoreStub) putObject(w http.ResponseWriter, r *http.Request) {
	o.m.Lock()
	defer o.m.Unlock()

	objectPath := r.URL.Path

	storagePath := path.Join(o.storage, objectPath)
	dir := path.Dir(storagePath)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}

	file, err := os.Create(storagePath)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}
	defer file.Close()

	hasher := md5.New()

	writers := io.MultiWriter(file, hasher)
	_, err = io.Copy(writers, r.Body)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}

	etag, overwritten := o.overwriteMD5[objectPath]
	if !overwritten {
		checksum := hasher.Sum(nil)
		etag = hex.EncodeToString(checksum)
	}

	o.puts++
	o.bucket[objectPath] = etag

	w.Header().Set("ETag", etag)
	w.WriteHeader(200)
}

func (o *ObjectstoreStub) getObject(w http.ResponseWriter, r *http.Request) {
	o.m.Lock()
	defer o.m.Unlock()

	objectPath := r.URL.Path

	etag := o.bucket[objectPath]
	if etag == "" {
		w.WriteHeader(404)
		return
	}

	w.Header().Set("ETag", etag)
	http.ServeFile(w, r, path.Join(o.storage, objectPath))
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
	case "GET":
		o.getObject(w, r)
	default:
		w.WriteHeader(404)
	}
}
