package testhelper

import (
	pb "gitlab.com/gitlab-org/gitaly/protos/go"
)

type GitalyTestServer struct{}

const GitalyInfoRefsResponseMock = "Mock Gitaly InfoRefsResponse data"

func NewGitalyServer() *GitalyTestServer {
	return &GitalyTestServer{}
}

func (s *GitalyTestServer) InfoRefsUploadPack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsUploadPackServer) error {
	response := &pb.InfoRefsResponse{
		Data: []byte(GitalyInfoRefsResponseMock),
	}
	return stream.Send(response)
}

func (s *GitalyTestServer) InfoRefsReceivePack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsReceivePackServer) error {
	response := &pb.InfoRefsResponse{
		Data: []byte(GitalyInfoRefsResponseMock),
	}
	return stream.Send(response)
}
