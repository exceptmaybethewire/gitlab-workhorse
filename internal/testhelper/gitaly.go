package testhelper

import (
	"fmt"
	"io"
	"io/ioutil"
	"path"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type GitalyTestServer struct {
	finalMessageCode codes.Code
	sync.WaitGroup
}

var (
	GitalyInfoRefsResponseMock    = strings.Repeat("Mock Gitaly InfoRefsResponse data", 100000)
	GitalyGetBlobResponseMock     = strings.Repeat("Mock Gitaly GetBlobResponse data", 100000)
	GitalyGetArchiveResponseMock  = strings.Repeat("Mock Gitaly GetArchiveResponse data", 100000)
	GitalyGetDiffResponseMock     = strings.Repeat("Mock Gitaly GetDiffResponse data", 100000)
	GitalyGetPatchResponseMock    = strings.Repeat("Mock Gitaly GetPatchResponse data", 100000)
	GitalyReceivePackResponseMock []byte
	GitalyUploadPackResponseMock  []byte
)

func init() {
	var err error
	if GitalyReceivePackResponseMock, err = ioutil.ReadFile(path.Join(RootDir(), "testdata/receive-pack-fixture.txt")); err != nil {
		log.Fatal(err)
	}
	if GitalyUploadPackResponseMock, err = ioutil.ReadFile(path.Join(RootDir(), "testdata/upload-pack-fixture.txt")); err != nil {
		log.Fatal(err)
	}
}

func NewGitalyServer(finalMessageCode codes.Code) *GitalyTestServer {
	return &GitalyTestServer{finalMessageCode: finalMessageCode}
}

func (s *GitalyTestServer) InfoRefsUploadPack(in *pb.InfoRefsRequest, stream pb.SmartHTTPService_InfoRefsUploadPackServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	if err := validateRepository(in.GetRepository()); err != nil {
		return err
	}

	fmt.Printf("Result: %+v", in)

	data := []byte(strings.Join([]string{
		strings.Join(in.GitConfigOptions, "\n") + "\n",
		GitalyInfoRefsResponseMock,
	}, "\000") + "\000")

	nSends, err := sendBytes(data, 100, func(p []byte) error {
		return stream.Send(&pb.InfoRefsResponse{Data: p})
	})
	if err != nil {
		return err
	}
	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) InfoRefsReceivePack(in *pb.InfoRefsRequest, stream pb.SmartHTTPService_InfoRefsReceivePackServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	if err := validateRepository(in.GetRepository()); err != nil {
		return err
	}

	response := &pb.InfoRefsResponse{
		Data: []byte(GitalyInfoRefsResponseMock),
	}
	if err := stream.Send(response); err != nil {
		return err
	}

	return s.finalError()
}

func (s *GitalyTestServer) PostReceivePack(stream pb.SmartHTTPService_PostReceivePackServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	req, err := stream.Recv()
	if err != nil {
		return err
	}

	repo := req.GetRepository()
	if err := validateRepository(req.GetRepository()); err != nil {
		return err
	}

	data := []byte(strings.Join([]string{
		repo.GetStorageName(),
		repo.GetRelativePath(),
		req.GlId,
		req.GlUsername,
	}, "\000") + "\000")

	// The body of the request starts in the second message
	for {
		req, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}

		// We want to echo the request data back
		data = append(data, req.GetData()...)
	}

	nSends, err := sendBytes(data, 100, func(p []byte) error {
		return stream.Send(&pb.PostReceivePackResponse{Data: p})
	})

	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) PostUploadPack(stream pb.SmartHTTPService_PostUploadPackServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	req, err := stream.Recv()
	if err != nil {
		return err
	}

	repo := req.GetRepository()
	if err := validateRepository(req.GetRepository()); err != nil {
		return err
	}

	data := []byte(strings.Join([]string{
		repo.GetStorageName(),
		repo.GetRelativePath(),
		strings.Join(req.GitConfigOptions, "\n") + "\n",
	}, "\000") + "\000")

	// The body of the request starts in the second message
	for {
		req, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}

		data = append(data, req.GetData()...)
	}

	nSends, err := sendBytes(data, 100, func(p []byte) error {
		return stream.Send(&pb.PostUploadPackResponse{Data: p})
	})

	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) CommitIsAncestor(ctx context.Context, in *pb.CommitIsAncestorRequest) (*pb.CommitIsAncestorResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) GetBlob(in *pb.GetBlobRequest, stream pb.BlobService_GetBlobServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	if err := validateRepository(in.GetRepository()); err != nil {
		return err
	}

	response := &pb.GetBlobResponse{
		Oid:  in.GetOid(),
		Size: int64(len(GitalyGetBlobResponseMock)),
	}
	nSends, err := sendBytes([]byte(GitalyGetBlobResponseMock), 100, func(p []byte) error {
		response.Data = p

		if err := stream.Send(response); err != nil {
			return err
		}

		// Use a new response so we don't send other fields (Size, ...) over and over
		response = &pb.GetBlobResponse{}

		return nil
	})
	if err != nil {
		return err
	}
	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) GetArchive(in *pb.GetArchiveRequest, stream pb.RepositoryService_GetArchiveServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	if err := validateRepository(in.GetRepository()); err != nil {
		return err
	}

	nSends, err := sendBytes([]byte(GitalyGetArchiveResponseMock), 100, func(p []byte) error {
		return stream.Send(&pb.GetArchiveResponse{Data: p})
	})
	if err != nil {
		return err
	}
	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) RawDiff(in *pb.RawDiffRequest, stream pb.DiffService_RawDiffServer) error {
	nSends, err := sendBytes([]byte(GitalyGetDiffResponseMock), 100, func(p []byte) error {
		return stream.Send(&pb.RawDiffResponse{
			Data: p,
		})
	})
	if err != nil {
		return err
	}
	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) RawPatch(in *pb.RawPatchRequest, stream pb.DiffService_RawPatchServer) error {
	s.WaitGroup.Add(1)
	defer s.WaitGroup.Done()

	if err := validateRepository(in.GetRepository()); err != nil {
		return err
	}

	nSends, err := sendBytes([]byte(GitalyGetPatchResponseMock), 100, func(p []byte) error {
		return stream.Send(&pb.RawPatchResponse{
			Data: p,
		})
	})
	if err != nil {
		return err
	}
	if nSends <= 1 {
		panic("should have sent more than one message")
	}

	return s.finalError()
}

func (s *GitalyTestServer) RepositoryExists(context.Context, *pb.RepositoryExistsRequest) (*pb.RepositoryExistsResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) RepackIncremental(context.Context, *pb.RepackIncrementalRequest) (*pb.RepackIncrementalResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) RepackFull(context.Context, *pb.RepackFullRequest) (*pb.RepackFullResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) GarbageCollect(context.Context, *pb.GarbageCollectRequest) (*pb.GarbageCollectResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) RepositorySize(context.Context, *pb.RepositorySizeRequest) (*pb.RepositorySizeResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) ApplyGitattributes(context.Context, *pb.ApplyGitattributesRequest) (*pb.ApplyGitattributesResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) FetchRemote(context.Context, *pb.FetchRemoteRequest) (*pb.FetchRemoteResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) CreateRepository(context.Context, *pb.CreateRepositoryRequest) (*pb.CreateRepositoryResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) Exists(context.Context, *pb.RepositoryExistsRequest) (*pb.RepositoryExistsResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) HasLocalBranches(ctx context.Context, in *pb.HasLocalBranchesRequest) (*pb.HasLocalBranchesResponse, error) {
	return nil, nil
}

func (s *GitalyTestServer) CommitDelta(in *pb.CommitDeltaRequest, stream pb.DiffService_CommitDeltaServer) error {
	return nil
}

func (s *GitalyTestServer) CommitDiff(in *pb.CommitDiffRequest, stream pb.DiffService_CommitDiffServer) error {
	return nil
}

func (s *GitalyTestServer) CommitPatch(in *pb.CommitPatchRequest, stream pb.DiffService_CommitPatchServer) error {
	return nil
}

func (s *GitalyTestServer) GetBlobs(in *pb.GetBlobsRequest, stream pb.BlobService_GetBlobsServer) error {
	return nil
}

// sendBytes returns the number of times the 'sender' function was called and an error.
func sendBytes(data []byte, chunkSize int, sender func([]byte) error) (int, error) {
	i := 0
	for ; len(data) > 0; i++ {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}

		if err := sender(data[:n]); err != nil {
			return i, err
		}
		data = data[n:]
	}

	return i, nil
}

func (s *GitalyTestServer) finalError() error {
	if code := s.finalMessageCode; code != codes.OK {
		return grpc.Errorf(code, "error as specified by test")
	}

	return nil
}

func validateRepository(repo *pb.Repository) error {
	if len(repo.GetStorageName()) == 0 {
		return fmt.Errorf("missing storage_name: %v", repo)
	}
	if len(repo.GetRelativePath()) == 0 {
		return fmt.Errorf("missing relative_path: %v", repo)
	}
	return nil
}
