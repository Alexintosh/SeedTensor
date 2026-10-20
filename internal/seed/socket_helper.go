package seed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// SocketHelper implements Helper by speaking a line-delimited JSON protocol to
// a libtorrent helper process over a unix domain socket.
//
// Protocol (one JSON object per line, request then response):
//
//	-> {"cmd":"add","magnet":"...","data_dir":"...","network":"clearnet","info_hash_v2":"..."}
//	<- {"ok":true}
//	-> {"cmd":"pause"}                      <- {"ok":true}
//	-> {"cmd":"resume"}                     <- {"ok":true}
//	-> {"cmd":"uploaded"}                   <- {"ok":true,"uploaded":12345}
//	-> {"cmd":"peers"}                      <- {"ok":true,"peers":4}
//	-> {"cmd":"close"}                      <- {"ok":true}
//
// On any error the response is {"ok":false,"error":"..."}. The helper is
// responsible for seeding files in place (never re-downloading) and for
// applying the network overlay named in the add command.
type SocketHelper struct {
	addr string

	mu   sync.Mutex
	conn net.Conn
	rw   *bufio.ReadWriter
}

// DialSocketHelper connects to the helper's unix socket.
func DialSocketHelper(ctx context.Context, socketPath string) (*SocketHelper, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial helper socket %s: %w", socketPath, err)
	}
	return &SocketHelper{
		addr: socketPath,
		conn: conn,
		rw:   bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
	}, nil
}

type helperReq struct {
	Cmd        string `json:"cmd"`
	Magnet     string `json:"magnet,omitempty"`
	DataDir    string `json:"data_dir,omitempty"`
	Network    string `json:"network,omitempty"`
	InfoHashV2 string `json:"info_hash_v2,omitempty"`
}

type helperResp struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Uploaded int64  `json:"uploaded,omitempty"`
	Peers    int    `json:"peers,omitempty"`
}

func (h *SocketHelper) call(ctx context.Context, req helperReq) (helperResp, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if dl, ok := ctx.Deadline(); ok {
		_ = h.conn.SetDeadline(dl)
	} else {
		_ = h.conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	defer h.conn.SetDeadline(time.Time{})

	line, err := json.Marshal(req)
	if err != nil {
		return helperResp{}, err
	}
	if _, err := h.rw.Write(append(line, '\n')); err != nil {
		return helperResp{}, fmt.Errorf("write %s: %w", req.Cmd, err)
	}
	if err := h.rw.Flush(); err != nil {
		return helperResp{}, fmt.Errorf("flush %s: %w", req.Cmd, err)
	}
	respLine, err := h.rw.ReadBytes('\n')
	if err != nil {
		return helperResp{}, fmt.Errorf("read %s response: %w", req.Cmd, err)
	}
	var resp helperResp
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return helperResp{}, fmt.Errorf("decode %s response: %w", req.Cmd, err)
	}
	if !resp.OK {
		return resp, fmt.Errorf("helper %s failed: %s", req.Cmd, resp.Error)
	}
	return resp, nil
}

func (h *SocketHelper) AddTorrent(ctx context.Context, t Torrent) error {
	_, err := h.call(ctx, helperReq{
		Cmd:        "add",
		Magnet:     t.Magnet,
		DataDir:    t.DataDir,
		Network:    t.Network,
		InfoHashV2: t.InfoHashV2,
	})
	return err
}

func (h *SocketHelper) Pause(ctx context.Context) error {
	_, err := h.call(ctx, helperReq{Cmd: "pause"})
	return err
}

func (h *SocketHelper) Resume(ctx context.Context) error {
	_, err := h.call(ctx, helperReq{Cmd: "resume"})
	return err
}

func (h *SocketHelper) UploadedBytes(ctx context.Context) (int64, error) {
	resp, err := h.call(ctx, helperReq{Cmd: "uploaded"})
	return resp.Uploaded, err
}

func (h *SocketHelper) Peers(ctx context.Context) (int, error) {
	resp, err := h.call(ctx, helperReq{Cmd: "peers"})
	return resp.Peers, err
}

func (h *SocketHelper) Close(ctx context.Context) error {
	_, _ = h.call(ctx, helperReq{Cmd: "close"})
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn.Close()
}
