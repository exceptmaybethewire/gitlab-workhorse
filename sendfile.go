/*
The xSendFile middleware transparently sends static files in HTTP responses
via the X-Sendfile mechanism. All that is needed in the Rails code is the
'send_file' method.
*/

package main

import (
	"log"
	"net/http"
)

func newSendFileResponseModifier(rw http.ResponseWriter, req *http.Request) *responseModifier {
	var file string
	m := &responseModifier{rw: rw}

	m.wantModify = func() bool {
		file = m.Header().Get("X-Sendfile")
		m.Header().Del("X-Sendfile")

		return file != "" && m.status == http.StatusOK
	}

	m.modify = func() {
		log.Printf("Send file %q for %s %q", file, req.Method, req.RequestURI)
		content, fi, err := openFile(file)
		if err != nil {
			http.NotFound(m.rw, req)
			return
		}
		defer content.Close()

		http.ServeContent(m.rw, req, "", fi.ModTime(), content)
	}

	req.Header.Set("X-Sendfile-Type", "X-Sendfile")
	return m
}
