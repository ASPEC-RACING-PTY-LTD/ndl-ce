package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
)

// FilesCall is a typed jail operation sent to the agent.
type FilesCall struct {
	TargetKind     string
	TargetID       string
	JailRoot       string
	Action         string
	Path           string
	DestPath       string
	Mode           uint32
	UID            int
	GID            int
	ExpectedMtime  string
	ExpectedSHA256 string
}

// FilesPutCall begins a chunked write.
type FilesPutCall struct {
	TargetKind string
	TargetID   string
	JailRoot   string
	Path       string
	MaxBytes   int64
	Mode       uint32
}

// FilesGetCall reads one file.
type FilesGetCall struct {
	TargetKind string
	TargetID   string
	JailRoot   string
	Path       string
}

// TermOpen is the first AttachTerminal metadata frame.
type TermOpen struct {
	TargetKind string
	TargetID   string
	JailRoot   string
	CWD        string
}

// TermConn is a bidirectional nodal.term.v1 byte stream.
type TermConn interface {
	Send(frame []byte) error
	Recv() ([]byte, error)
	Close() error
}

func (c Client) FilesOp(ctx context.Context, call FilesCall) (json.RawMessage, error) {
	dest := call.DestPath
	if call.Action == "chown" && dest == "" {
		dest = fmt.Sprintf("%d:%d", call.UID, call.GID)
	}
	res, err := c.rpc().FilesOp(ctx, connect.NewRequest(&agentv1.FilesOpRequest{
		TargetKind: call.TargetKind, TargetId: call.TargetID, JailRoot: call.JailRoot,
		Action: call.Action, Path: call.Path, DestPath: dest, Mode: call.Mode,
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetResultJson(), nil
}

func (c Client) FilesPut(ctx context.Context, call FilesPutCall, r io.Reader, expectedSHA string) (json.RawMessage, error) {
	stream := c.rpc().FilesPut(ctx)
	if err := stream.Send(&agentv1.FilesPutRequest{Payload: &agentv1.FilesPutRequest_Begin{Begin: &agentv1.FilesPutBegin{
		TargetKind: call.TargetKind, TargetId: call.TargetID, JailRoot: call.JailRoot,
		Path: call.Path, MaxBytes: call.MaxBytes, Mode: call.Mode,
	}}}); err != nil {
		return nil, err
	}
	buf := make([]byte, filesChunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if serr := stream.Send(&agentv1.FilesPutRequest{Payload: &agentv1.FilesPutRequest_Chunk{Chunk: chunk}}); serr != nil {
				return nil, serr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if err := stream.Send(&agentv1.FilesPutRequest{Payload: &agentv1.FilesPutRequest_Finish{
		Finish: &agentv1.FilesPutFinish{ExpectedSha256: expectedSHA},
	}}); err != nil {
		return nil, err
	}
	res, err := stream.CloseAndReceive()
	if err != nil {
		return nil, err
	}
	return res.Msg.GetResultJson(), nil
}

func (c Client) FilesGet(ctx context.Context, call FilesGetCall) (io.ReadCloser, error) {
	stream, err := c.rpc().FilesGet(ctx, connect.NewRequest(&agentv1.FilesGetRequest{
		TargetKind: call.TargetKind, TargetId: call.TargetID, JailRoot: call.JailRoot, Path: call.Path,
	}))
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		for stream.Receive() {
			msg := stream.Msg()
			if chunk := msg.GetChunk(); len(chunk) > 0 {
				if _, err := pw.Write(chunk); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr, nil
}

// FilesGetBuffer reads the whole file. SHA is taken from the last response.
func (c Client) FilesGetBuffer(ctx context.Context, call FilesGetCall) ([]byte, string, error) {
	stream, err := c.rpc().FilesGet(ctx, connect.NewRequest(&agentv1.FilesGetRequest{
		TargetKind: call.TargetKind, TargetId: call.TargetID, JailRoot: call.JailRoot, Path: call.Path,
	}))
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	var sha string
	for stream.Receive() {
		msg := stream.Msg()
		if _, err := buf.Write(msg.GetChunk()); err != nil {
			return nil, "", err
		}
		if s := msg.GetSha256(); s != "" {
			sha = s
		}
	}
	if err := stream.Err(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), sha, nil
}

func (c Client) OpenTerminal(ctx context.Context, open TermOpen) (TermConn, error) {
	stream := c.rpc().AttachTerminal(ctx)
	if err := stream.Send(&agentv1.TermFrame{
		TargetKind: open.TargetKind,
		TargetId:   open.TargetID,
		JailRoot:   open.JailRoot,
		Cwd:        open.CWD,
	}); err != nil {
		return nil, err
	}
	return &agentTermConn{stream: stream}, nil
}

type agentTermConn struct {
	stream *connect.BidiStreamForClient[agentv1.TermFrame, agentv1.TermFrame]
}

func (t *agentTermConn) Send(frame []byte) error {
	if t.stream == nil {
		return fmt.Errorf("terminal stream is closed")
	}
	return t.stream.Send(&agentv1.TermFrame{Frame: frame})
}

func (t *agentTermConn) Recv() ([]byte, error) {
	msg, err := t.stream.Receive()
	if err != nil {
		return nil, err
	}
	return msg.GetFrame(), nil
}

func (t *agentTermConn) Close() error {
	if t.stream == nil {
		return nil
	}
	return t.stream.CloseResponse()
}
