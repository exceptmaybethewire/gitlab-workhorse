/*
In this file we handle git lfs objects downloads and uploads
*/

package lfs

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"context"
	"strconv"
	"net/url"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/file_storage"
)

func PutStore(a *api.API, h http.Handler) http.Handler {
	return lfsAuthorizeHandler(a, h)
}

func lfsAuthorizeHandler(myAPI *api.API, h http.Handler) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		fh, cancelFn, err := file_storage.SaveFile(context.Background(), a, r.Body, r.ContentLength)
		if err != nil {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLfsObject: copy body to tempfile: %v", err))
			return
		}
		defer cancelFn()

		if fh.Size != a.LfsSize {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLfsObject: expected size %d, wrote %d", a.LfsSize, fh.Size))
			return
		}

		if fh.Hashes["sha256"] != a.LfsOid {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLfsObject: expected sha256 %s, got %s", a.LfsOid,  fh.Hashes["sha256"]))
			return
		}

		data := url.Values{}
		data.Set("file.name", a.LfsOid)
		if fh.LocalPath != "" {
			data.Set("file.path", fh.LocalPath)
		}
		if fh.RemoteURL != "" {
			data.Set("file.store_url", fh.RemoteURL)
		}
		if fh.RemoteID != "" {
			data.Set("file.object_id", fh.RemoteID)
		}
		data.Set("file.size", strconv.FormatInt(fh.Size, 10))
		for hashName, hash := range fh.Hashes {
			data.Set("file." + hashName, hash)
		}
		dataString := data.Encode()

		r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		
		fmt.Println("DATA STRING", dataString)

		// Inject header and body
		r.Body = ioutil.NopCloser(bytes.NewBufferString(dataString))
		r.ContentLength = int64(len(dataString))
		fmt.Println("DATA XX", r.ContentLength)

		r.Body = nil
		r.ContentLength = 0

		r.URL.RawQuery += dataString

		// And proxy the request
		h.ServeHTTP(w, r)
	}, "/authorize")
}
