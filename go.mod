module github.com/no-dal/ndl-ce

go 1.24.0

require (
	connectrpc.com/connect v1.18.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.4
	golang.org/x/crypto v0.36.0
	golang.org/x/net v0.37.0
	golang.org/x/sys v0.31.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/text v0.23.0 // indirect
)

tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)
