package gitaly

import (
	"context"
	"fmt"
	"io"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

type SmartHTTPClient struct {
	pb.SmartHTTPServiceClient
}

func (client *SmartHTTPClient) InfoRefsResponseReader(ctx context.Context, repo *pb.Repository, rpc string, gitConfigOptions []string) (io.Reader, error) {
	rpcRequest := &pb.InfoRefsRequest{Repository: repo, GitConfigOptions: gitConfigOptions}

	switch rpc {
	case "git-upload-pack":
		stream, err := client.InfoRefsUploadPack(ctx, rpcRequest)
		return infoRefsReader(stream), err
	case "git-receive-pack":
		stream, err := client.InfoRefsReceivePack(ctx, rpcRequest)
		return infoRefsReader(stream), err
	default:
		return nil, fmt.Errorf("InfoRefsResponseWriterTo: Unsupported RPC: %q", rpc)
	}
}

type infoRefsClient interface {
	Recv() (*pb.InfoRefsResponse, error)
}

func infoRefsReader(stream infoRefsClient) io.Reader {
	return streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
}

func (client *SmartHTTPClient) ReceivePack(ctx context.Context, repo *pb.Repository, glId string, glUsername string, glRepository string, clientRequest io.Reader, clientResponse io.Writer) error {
	stream, err := client.PostReceivePack(ctx)
	if err != nil {
		return err
	}

	rpcRequest := &pb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         glId,
		GlUsername:   glUsername,
		GlRepository: glRepository,
	}

	if err := stream.Send(rpcRequest); err != nil {
		return fmt.Errorf("initial request: %v", err)
	}

	numStreams := 2
	errC := make(chan error, numStreams)

	go func() {
		rr := streamio.NewReader(func() ([]byte, error) {
			response, err := stream.Recv()
			return response.GetData(), err
		})
		_, err := io.Copy(clientResponse, rr)
		errC <- err
	}()

	go func() {
		sw := streamio.NewWriter(func(data []byte) error {
			return stream.Send(&pb.PostReceivePackRequest{Data: data})
		})
		_, err := io.Copy(sw, clientRequest)
		stream.CloseSend()
		errC <- err
	}()

	for i := 0; i < numStreams; i++ {
		if err := <-errC; err != nil {
			return err
		}
	}

	return nil
}

func (client *SmartHTTPClient) UploadPack(ctx context.Context, repo *pb.Repository, clientRequest io.Reader, clientResponse io.Writer, gitConfigOptions []string) error {
	stream, err := client.PostUploadPack(ctx)
	if err != nil {
		return err
	}

	rpcRequest := &pb.PostUploadPackRequest{
		Repository:       repo,
		GitConfigOptions: gitConfigOptions,
	}

	if err := stream.Send(rpcRequest); err != nil {
		return fmt.Errorf("initial request: %v", err)
	}

	numStreams := 2
	errC := make(chan error, numStreams)

	go func() {
		rr := streamio.NewReader(func() ([]byte, error) {
			response, err := stream.Recv()
			return response.GetData(), err
		})
		_, err := io.Copy(clientResponse, rr)
		errC <- err
	}()

	go func() {
		sw := streamio.NewWriter(func(data []byte) error {
			return stream.Send(&pb.PostUploadPackRequest{Data: data})
		})
		_, err := io.Copy(sw, clientRequest)
		stream.CloseSend()
		errC <- err
	}()

	for i := 0; i < numStreams; i++ {
		if err := <-errC; err != nil {
			return err
		}
	}

	return nil
}
