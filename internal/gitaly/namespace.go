package gitaly

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

// NamespaceClient encapsulates NamespaceService calls
type NamespaceClient struct {
	pb.NamespaceServiceClient
}
