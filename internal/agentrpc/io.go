package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/ndlterm"
)

func (h *Handler) FilesOp(ctx context.Context, req *connect.Request[agentv1.FilesOpRequest]) (*connect.Response[agentv1.FilesOpResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	root, err := h.resolveJail(req.Msg.GetTargetKind(), req.Msg.GetTargetId(), req.Msg.GetJailRoot())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if isGuestJail(root) {
		dest := req.Msg.GetDestPath()
		raw, err := h.guestFilesOp(ctx, req.Msg.GetTargetId(), req.Msg.GetAction(), req.Msg.GetPath(), dest, req.Msg.GetMode())
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.FilesOpResponse{Ok: true, Message: "ok", ResultJson: raw}), nil
	}
	raw, err := runFilesOp(root, req.Msg.GetAction(), req.Msg.GetPath(), req.Msg.GetDestPath(), req.Msg.GetMode())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.FilesOpResponse{Ok: true, Message: "ok", ResultJson: raw}), nil
}

func (h *Handler) FilesPut(ctx context.Context, stream *connect.ClientStream[agentv1.FilesPutRequest]) (*connect.Response[agentv1.FilesOpResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, err
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("empty FilesPut stream"))
	}
	begin := stream.Msg().GetBegin()
	if begin == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("FilesPut begin is required"))
	}
	root, err := h.resolveJail(begin.GetTargetKind(), begin.GetTargetId(), begin.GetJailRoot())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if isGuestJail(root) {
		var buf []byte
		var expected string
		for stream.Receive() {
			msg := stream.Msg()
			if chunk := msg.GetChunk(); len(chunk) > 0 {
				if len(chunk) > filesChunk || len(buf)+len(chunk) > filesChunk {
					return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("chunk exceeds 8 MiB"))
				}
				buf = append(buf, chunk...)
			}
			if fin := msg.GetFinish(); fin != nil {
				expected = fin.GetExpectedSha256()
			}
		}
		if err := stream.Err(); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		raw, err := h.guestFilesPut(ctx, begin.GetTargetId(), begin.GetPath(), begin.GetMode(), buf)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if expected != "" && !shaMatches(raw, expected) {
			_, _ = h.guestFilesOp(ctx, begin.GetTargetId(), "delete", begin.GetPath(), "", 0)
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sha256 mismatch"))
		}
		return connect.NewResponse(&agentv1.FilesOpResponse{Ok: true, Message: "ok", ResultJson: raw}), nil
	}
	pr, pw := io.Pipe()
	type putFinish struct {
		sha string
		err error
	}
	finCh := make(chan putFinish, 1)
	go func() {
		var expected string
		for stream.Receive() {
			msg := stream.Msg()
			if chunk := msg.GetChunk(); len(chunk) > 0 {
				if len(chunk) > filesChunk {
					err := fmt.Errorf("chunk exceeds 8 MiB")
					_ = pw.CloseWithError(err)
					finCh <- putFinish{err: err}
					return
				}
				if _, err := pw.Write(chunk); err != nil {
					finCh <- putFinish{err: err}
					return
				}
			}
			if fin := msg.GetFinish(); fin != nil {
				expected = fin.GetExpectedSha256()
			}
		}
		if err := stream.Err(); err != nil {
			_ = pw.CloseWithError(err)
			finCh <- putFinish{err: err}
			return
		}
		_ = pw.Close()
		finCh <- putFinish{sha: expected}
	}()
	raw, err := writePartThenRename(root, begin.GetPath(), begin.GetMode(), pr, begin.GetMaxBytes(), "")
	if err != nil {
		_ = pr.Close()
	}
	fin := <-finCh
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if fin.err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fin.err)
	}
	if fin.sha != "" && !shaMatches(raw, fin.sha) {
		_, _ = runFilesOp(root, "delete", begin.GetPath(), "", 0)
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sha256 mismatch"))
	}
	_ = ctx
	return connect.NewResponse(&agentv1.FilesOpResponse{Ok: true, Message: "ok", ResultJson: raw}), nil
}

func shaMatches(raw []byte, expected string) bool {
	want := strings.TrimSpace(expected)
	if want == "" {
		return true
	}
	var out struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	got := strings.TrimSpace(out.SHA256)
	return got != "" && strings.EqualFold(got, want)
}

func (h *Handler) FilesGet(ctx context.Context, req *connect.Request[agentv1.FilesGetRequest], stream *connect.ServerStream[agentv1.FilesGetResponse]) error {
	if err := h.authorize(ctx); err != nil {
		return err
	}
	root, err := h.resolveJail(req.Msg.GetTargetKind(), req.Msg.GetTargetId(), req.Msg.GetJailRoot())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if isGuestJail(root) {
		data, sha, err := h.guestFilesGet(ctx, req.Msg.GetTargetId(), req.Msg.GetPath())
		if err != nil {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return stream.Send(&agentv1.FilesGetResponse{Chunk: data, Sha256: sha})
	}
	err = readFileSHA(root, req.Msg.GetPath(), func(chunk []byte, sha string) error {
		return stream.Send(&agentv1.FilesGetResponse{Chunk: chunk, Sha256: sha})
	})
	if err != nil {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return nil
}

func (h *Handler) AttachTerminal(ctx context.Context, stream *connect.BidiStream[agentv1.TermFrame, agentv1.TermFrame]) error {
	if err := h.authorize(ctx); err != nil {
		return err
	}
	first, err := stream.Receive()
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	root, err := h.resolveJail(first.GetTargetKind(), first.GetTargetId(), first.GetJailRoot())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	sess, err := startTermSession(ctx, h, termRequest{
		TargetKind: first.GetTargetKind(),
		TargetID:   first.GetTargetId(),
		JailRoot:   root,
		CWD:        first.GetCwd(),
		LXCPath:    lxcRuntimePath(h.Workloads),
	})
	if err != nil {
		_ = sendTerm(stream, first, ndlterm.TypeError, []byte(err.Error()))
		_ = sendTerm(stream, first, ndlterm.TypeSessionEnded, []byte("failed"))
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	defer sess.Close()
	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := sess.Read(buf)
			if n > 0 {
				if err := sendTerm(stream, first, ndlterm.TypeOutput, buf[:n]); err != nil {
					errCh <- err
					return
				}
			}
			if rerr != nil {
				errCh <- rerr
				return
			}
		}
	}()
	go func() {
		for {
			cwd, ok := sess.CWD()
			if ok {
				_ = sendTerm(stream, first, ndlterm.TypeCWD, []byte(jailRelCWD(root, cwd)))
			}
			select {
			case <-ctx.Done():
				return
			case <-sess.Done():
				return
			case <-cwdTick():
			}
		}
	}()
	if len(first.GetFrame()) > 0 {
		if err := applyTermFrame(sess, first.GetFrame()); err != nil && !errors.Is(err, errIgnoreFrame) {
			_ = sendTerm(stream, first, ndlterm.TypeError, []byte(err.Error()))
		}
	}
	go func() {
		for {
			msg, rerr := stream.Receive()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if err := applyTermFrame(sess, msg.GetFrame()); err != nil && !errors.Is(err, errIgnoreFrame) {
				if errors.Is(err, errNeedPong) {
					_ = sendTerm(stream, first, ndlterm.TypePong, nil)
					continue
				}
				_ = sendTerm(stream, first, ndlterm.TypeError, []byte(err.Error()))
			}
		}
	}()
	select {
	case <-ctx.Done():
		_ = sendTerm(stream, first, ndlterm.TypeSessionEnded, []byte("context canceled"))
		return ctx.Err()
	case err := <-errCh:
		_ = sendTerm(stream, first, ndlterm.TypeSessionEnded, []byte("session ended"))
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			return nil
		}
		return nil
	case <-sess.Done():
		_ = sendTerm(stream, first, ndlterm.TypeSessionEnded, []byte("session ended"))
		return nil
	}
}

func sendTerm(stream *connect.BidiStream[agentv1.TermFrame, agentv1.TermFrame], meta *agentv1.TermFrame, typ byte, payload []byte) error {
	raw, err := ndlterm.Encode(typ, payload)
	if err != nil {
		return err
	}
	return stream.Send(&agentv1.TermFrame{
		TargetKind: meta.GetTargetKind(),
		TargetId:   meta.GetTargetId(),
		JailRoot:   meta.GetJailRoot(),
		Cwd:        meta.GetCwd(),
		Frame:      raw,
	})
}

var errIgnoreFrame = errors.New("ignore")

func applyTermFrame(sess termSession, raw []byte) error {
	if len(raw) == 0 {
		return errIgnoreFrame
	}
	fr, err := ndlterm.Decode(raw)
	if err != nil {
		return err
	}
	switch fr.Type {
	case ndlterm.TypeInput:
		_, err := sess.Write(fr.Payload)
		return err
	case ndlterm.TypeResize:
		rows, cols, err := ndlterm.ParseResize(fr.Payload)
		if err != nil {
			return err
		}
		return sess.Resize(rows, cols)
	case ndlterm.TypePing:
		if err := sess.Pong(); err != nil {
			return err
		}
		return errNeedPong
	default:
		return errIgnoreFrame
	}
}

var errNeedPong = errors.New("pong")
