package main

import (
	"net/http"
)

type responseModifier struct {
	rw         http.ResponseWriter
	status     int
	modified   bool
	wantModify func() bool
	modify     func()
}

func (m *responseModifier) Header() http.Header {
	return m.rw.Header()
}

func (m *responseModifier) Flush() {
	m.WriteHeader(http.StatusOK)
}

func (m *responseModifier) Write(data []byte) (n int, err error) {
	if m.status == 0 {
		m.WriteHeader(http.StatusOK)
	}
	if m.modified {
		return
	}
	return m.rw.Write(data)
}

func (m *responseModifier) WriteHeader(status int) {
	if m.status != 0 {
		return
	}

	m.status = status

	if !m.wantModify() {
		m.rw.WriteHeader(m.status)
		return
	}

	m.modified = true
	m.modify()
}
