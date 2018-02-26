package client

import (
	"io"

	"gitlab.com/gitlab-org/gitaly/streamio"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// ReceivePack proxies an SSH git-receive-pack (git push) session to Gitaly
func ReceivePack(ctx context.Context, conn *grpc.ClientConn, stdin io.Reader, stdout, stderr io.Writer, req *pb.SSHReceivePackRequest) (int32, error) {
	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	ssh := pb.NewSSHServiceClient(conn)
	stream, err := ssh.SSHReceivePack(ctx2)
	if err != nil {
		return 0, err
	}

	if err = stream.Send(req); err != nil {
		return 0, err
	}

	inWriter := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&pb.SSHReceivePackRequest{Stdin: p})
	})

	return streamHandler(func() (stdoutStderrResponse, error) {
		return stream.Recv()
	}, func(errC chan error) {
		_, errRecv := io.Copy(inWriter, stdin)
		stream.CloseSend()
		errC <- errRecv
	}, stdout, stderr)
}
