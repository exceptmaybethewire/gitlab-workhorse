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
		// Check X-Sendfile header
		file = m.Header().Get("X-Sendfile")
		m.Header().Del("X-Sendfile")

		// If file is empty or status is not 200 pass through header
		return file != "" && m.status == http.StatusOK
	}

	m.modify = func() {
		// Serve the file
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
