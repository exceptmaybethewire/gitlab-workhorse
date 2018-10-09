package contentdisposition

import "net/http"

type contentDisposition struct {
	rw http.ResponseWriter
}

func NewSWFBlocker(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cd := &contentDisposition{
			rw: w,
		}
		defer cd.Flush()

		h.ServeHTTP(cd, r)
	})
}

func (cd *contentDisposition) Flush()                      {}
func (cd *contentDisposition) Write(p []byte) (int, error) { return cd.rw.Write(p) }
func (cd *contentDisposition) WriteHeader(status int)      {}
func (cd *contentDisposition) Header() http.Header         { return cd.rw.Header() }
