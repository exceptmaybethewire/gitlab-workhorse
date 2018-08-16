package git

import (
	"fmt"
	"io"
	"net/http"

	"github.com/golang/protobuf/jsonpb"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/gitaly"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
)

type snapshot struct {
	senddata.Prefix
}

type snapshotParams struct {
	GitalyServer       gitaly.Server
	GetSnapshotRequest string
}

var (
	SendSnapshot = &snapshot{"git-snapshot:"}
)

func (s *snapshot) Inject(w http.ResponseWriter, r *http.Request, sendData string) {
	var params snapshotParams

	if err := s.Unpack(&params, sendData); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendSnapshot: unpack sendData: %v", err))
		return
	}

	request := &pb.GetSnapshotRequest{}
	if err := jsonpb.UnmarshalString(params.GetSnapshotRequest, request); err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendSnapshot: unmarshal GetSnapshotRequest: %v", err))
		return
	}

	c, err := gitaly.NewRepositoryClient(params.GitalyServer)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendSnapshot: gitaly.NewRepositoryClient: %v", err))
		return
	}

	reader, err := c.SnapshotReader(r.Context(), request)
	if err != nil {
		helper.Fail500(w, r, fmt.Errorf("SendSnapshot: client.SnapshotReader: %v", err))
		return
	}

	w.Header().Del("Content-Length")
	w.Header().Set("Content-Disposition", `attachment; filename="snapshot.tar"`)
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Transfer-Encoding", "binary")
	w.Header().Set("Cache-Control", "private")
	w.WriteHeader(http.StatusOK) // Errors aren't detectable beyond this point

	if _, err := io.Copy(w, reader); err != nil {
		helper.LogError(r, fmt.Errorf("SendSnapshot: copy gitaly output: %v", err))
	}
}
