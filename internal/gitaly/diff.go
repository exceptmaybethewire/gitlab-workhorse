package gitaly

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/gitlab-org/gitaly/streamio"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type DiffClient struct {
	pb.DiffServiceClient
}

func (client *DiffClient) SendRawDiff(ctx context.Context, w http.ResponseWriter, request *pb.RawDiffRequest) error {
	c, err := client.RawDiff(ctx, request)
	if err != nil {
		return fmt.Errorf("rpc failed: %v", err)
	}

	w.Header().Del("Content-Length")

	rr := streamio.NewReader(func() ([]byte, error) {
		resp, err := c.Recv()
		return resp.GetData(), err
	})

	if _, err := io.Copy(w, rr); err != nil {
		return fmt.Errorf("copy rpc data: %v", err)
	}

	return nil
}

func (client *DiffClient) SendRawPatch(ctx context.Context, w http.ResponseWriter, request *pb.RawPatchRequest) error {
	c, err := client.RawPatch(ctx, request)
	if err != nil {
		return fmt.Errorf("rpc failed: %v", err)
	}

	w.Header().Del("Content-Length")

	rr := streamio.NewReader(func() ([]byte, error) {
		resp, err := c.Recv()
		return resp.GetData(), err
	})

	if _, err := io.Copy(w, rr); err != nil {
		return fmt.Errorf("copy rpc data: %v", err)
	}

	return nil
}
