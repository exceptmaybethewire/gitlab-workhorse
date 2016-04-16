package builds

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

func readRawTrace(traceFile string, headers http.Header, output io.Writer) error {
	file, err := os.Open(traceFile)
	defer file.Close()

	if err != nil {
		return err
	}

	reader := bufio.NewReader(file)

	headers.Set("Content-Type", "text/plain; charset=utf-8")
	_, err = io.Copy(output, reader)

	if err != nil {
		return fmt.Errorf("Copy stdout of %v: %v", traceFile, err)
	}

	return nil
}

func RawTrace(myApi *api.API) http.Handler {
	return myApi.PreAuthorizeHandler(func(writer http.ResponseWriter, req *http.Request, api *api.Response) {
		if api.TraceFile == "" {
			helper.Fail500(writer, errors.New("RawTrace: TraceFile is empty"))
			return
		}

		err := readRawTrace(api.TraceFile, writer.Header(), writer)
		if os.IsNotExist(err) {
			http.NotFound(writer, req)
			return
		} else if err != nil {
			helper.Fail500(writer, fmt.Errorf("RawTrace: %v", err))
		}
	}, "")
}
