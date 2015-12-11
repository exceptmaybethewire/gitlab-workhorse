package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
)

func newErrorPageResponseModifier(w http.ResponseWriter, path *string) *responseModifier {
	var data []byte
	m := &responseModifier{rw: w}

	m.wantModify = func() bool {
		if 400 <= m.status && m.status <= 599 {
			var err error
			errorPageFile := filepath.Join(*path, fmt.Sprintf("%d.html", m.status))
			if data, err = ioutil.ReadFile(errorPageFile); err == nil {
				return true
			}
		}
		return false
	}

	m.modify = func() {
		log.Printf("ErrorPage: serving predefined error page: %d", m.status)
		setNoCacheHeaders(m.rw.Header())
		m.rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		m.rw.WriteHeader(m.status)
		m.rw.Write(data)
	}

	return m
}

func handleRailsError(documentRoot *string, handler serviceHandleFunc) serviceHandleFunc {
	return func(w http.ResponseWriter, r *gitRequest) {
		m := newErrorPageResponseModifier(w, documentRoot)
		defer m.Flush()
		handler(m, r)
	}
}
