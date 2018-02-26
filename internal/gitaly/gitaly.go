package gitaly

import (
	"sync"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/auth"
	gitalyclient "gitlab.com/gitlab-org/gitaly/client"
	"google.golang.org/grpc"
)

type Server struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

type connectionsCache struct {
	sync.RWMutex
	connections map[Server]*grpc.ClientConn
}

var cache = connectionsCache{
	connections: make(map[Server]*grpc.ClientConn),
}

func NewSmartHTTPClient(server Server) (*SmartHTTPClient, error) {
	conn, err := getOrCreateConnection(server)
	if err != nil {
		return nil, err
	}
	grpcClient := pb.NewSmartHTTPServiceClient(conn)
	return &SmartHTTPClient{grpcClient}, nil
}

func NewBlobClient(server Server) (*BlobClient, error) {
	conn, err := getOrCreateConnection(server)
	if err != nil {
		return nil, err
	}
	grpcClient := pb.NewBlobServiceClient(conn)
	return &BlobClient{grpcClient}, nil
}

func NewRepositoryClient(server Server) (*RepositoryClient, error) {
	conn, err := getOrCreateConnection(server)
	if err != nil {
		return nil, err
	}
	grpcClient := pb.NewRepositoryServiceClient(conn)
	return &RepositoryClient{grpcClient}, nil
}

func NewDiffClient(server Server) (*DiffClient, error) {
	conn, err := getOrCreateConnection(server)
	if err != nil {
		return nil, err
	}
	grpcClient := pb.NewDiffServiceClient(conn)
	return &DiffClient{grpcClient}, nil
}

func getOrCreateConnection(server Server) (*grpc.ClientConn, error) {
	cache.RLock()
	conn := cache.connections[server]
	cache.RUnlock()

	if conn != nil {
		return conn, nil
	}

	cache.Lock()
	defer cache.Unlock()

	if conn := cache.connections[server]; conn != nil {
		return conn, nil
	}

	conn, err := newConnection(server)
	if err != nil {
		return nil, err
	}

	cache.connections[server] = conn

	return conn, nil
}

func CloseConnections() {
	cache.Lock()
	defer cache.Unlock()

	for _, conn := range cache.connections {
		conn.Close()
	}
}

func newConnection(server Server) (*grpc.ClientConn, error) {
	connOpts := append(gitalyclient.DefaultDialOpts,
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(server.Token)),
		grpc.WithStreamInterceptor(grpc_prometheus.StreamClientInterceptor),
		grpc.WithUnaryInterceptor(grpc_prometheus.UnaryClientInterceptor),
	)

	return gitalyclient.Dial(server.Address, connOpts)
}
