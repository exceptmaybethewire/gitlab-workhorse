package helper

import (
	"net/http"
	"reflect"

	"github.com/getsentry/raven-go"
)

var ravenHeaderBlacklist = []string{
	"Authorization",
	"Private-Token",
}

func captureRavenError(r *http.Request, err error) {
	client := raven.DefaultClient

	interfaces := []raven.Interface{}
	if r != nil {
		CleanHeadersForRaven(r)
		interfaces = append(interfaces, raven.NewHttp(r))
	}

	exception := &raven.Exception{
		Stacktrace: raven.NewStacktrace(2, 3, nil),
		Value:      err.Error(),
		Type:       reflect.TypeOf(err).String(),
	}
	interfaces = append(interfaces, exception)

	packet := raven.NewPacket(err.Error(), interfaces...)
	client.Capture(packet, nil)
}

func CleanHeadersForRaven(r *http.Request) {
	if r == nil {
		return
	}

	for _, key := range ravenHeaderBlacklist {
		if r.Header.Get(key) != "" {
			r.Header.Set(key, "[redacted]")
		}
	}
}
