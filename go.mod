module github.com/no-dal/ndl-ce

go 1.24.0

require (
	connectrpc.com/connect v1.18.1
	google.golang.org/protobuf v1.36.6
)

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)
