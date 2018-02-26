/*
In this file we handle git lfs objects downloads and uploads
*/

package lfs

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

func PutStore(a *api.API, h http.Handler) http.Handler {
	return handleStoreLFSObject(a, h)
}

func handleStoreLFSObject(myAPI *api.API, h http.Handler) http.Handler {
	return myAPI.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		opts := filestore.GetOpts(a)
		opts.TempFilePrefix = a.LfsOid

		// backward compatible api check - to be removed on next release
		if a.StoreLFSPath != "" {
			opts.LocalTempPath = a.StoreLFSPath
		}
		// end of backward compatible api check

		fh, err := filestore.SaveFileFromReader(r.Context(), r.Body, r.ContentLength, opts)
		if err != nil {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLFSObject: copy body to tempfile: %v", err))
			return
		}

		if fh.Size != a.LfsSize {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLFSObject: expected size %d, wrote %d", a.LfsSize, fh.Size))
			return
		}

		if fh.SHA256() != a.LfsOid {
			helper.Fail500(w, r, fmt.Errorf("handleStoreLFSObject: expected sha256 %s, got %s", a.LfsOid, fh.SHA256()))
			return
		}

		data := url.Values{}
		for k, v := range fh.GitLabFinalizeFields("file") {
			data.Set(k, v)
		}

		// Hijack body
		body := data.Encode()
		r.Body = ioutil.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// backward compatible API header - to be removed on next release
		if opts.IsLocal() {
			r.Header.Set("X-GitLab-Lfs-Tmp", filepath.Base(fh.LocalPath))
		}
		// end of backward compatible API header

		// And proxy the request
		h.ServeHTTP(w, r)
	}, "/authorize")
}
