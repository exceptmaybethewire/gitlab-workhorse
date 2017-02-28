package gitaly

import (
	"net"
	"sync"
	"time"

	pb "gitlab.com/gitlab-org/gitaly/protos/go"
	"google.golang.org/grpc"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
)

type connectionsCache struct {
	sync.RWMutex
	connections map[string]*grpc.ClientConn
}

var cache = connectionsCache{
	connections: make(map[string]*grpc.ClientConn),
}

func NewSmartHTTPClient(socketPath string) (*SmartHTTPClient, error) {
	conn, err := getOrCreateConnection(socketPath)
	if err != nil {
		return nil, err
	}
	grpcClient := pb.NewSmartHTTPClient(conn)
	return &SmartHTTPClient{grpcClient}, nil
}

func getOrCreateConnection(socketPath string) (*grpc.ClientConn, error) {
	cache.Lock()
	defer cache.Unlock()

	if conn := cache.connections[socketPath]; conn != nil {
		return conn, nil
	}

	conn, err := newConnection(socketPath)
	if err != nil {
		return nil, err
	}

	cache.connections[socketPath] = conn

	return conn, nil
}

func CloseConnections() {
	cache.Lock()
	defer cache.Unlock()

	for _, conn := range cache.connections {
		conn.Close()
	}
}

func newConnection(socketPath string) (*grpc.ClientConn, error) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(), // Since we're connecting to Gitaly over UNIX, we don't need to use TLS credentials.
		grpc.WithDialer(func(addr string, _ time.Duration) (net.Conn, error) {
			return badgateway.DefaultDialer.Dial("unix", addr)
		}),
	}
	conn, err := grpc.Dial(socketPath, connOpts...)
	if err != nil {
		return nil, err
	}

	return conn, nil
}
