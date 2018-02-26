package gitaly

import (
	"context"
	"fmt"
	"io"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

// RepositoryClient encapsulates RepositoryService calls
type RepositoryClient struct {
	pb.RepositoryServiceClient
}

// ArchiveReader performs a GetArchive Gitaly request and returns an io.Reader
// for the response
func (client *RepositoryClient) ArchiveReader(ctx context.Context, request *pb.GetArchiveRequest) (io.Reader, error) {
	c, err := client.GetArchive(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("RepositoryService::GetArchive: %v", err)
	}

	return streamio.NewReader(func() ([]byte, error) {
		resp, err := c.Recv()

		return resp.GetData(), err
	}), nil
}
