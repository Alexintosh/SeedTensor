package seed

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
)

// fakeSocketServer speaks the helper protocol over a unix socket, backed by a
// fakeHelper. It lets us exercise the real SocketHelper wire code.
type fakeSocketServer struct {
	ln   net.Listener
	back *fakeHelper
	wg   sync.WaitGroup
}

func startFakeSocketServer(t *testing.T) *fakeSocketServer {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "helper.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSocketServer{ln: ln, back: &fakeHelper{}}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	t.Cleanup(func() { ln.Close(); s.wg.Wait() })
	return s
}

func (s *fakeSocketServer) path() string { return s.ln.Addr().String() }

func (s *fakeSocketServer) serve(conn net.Conn) {
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	for {
		line, err := rw.ReadBytes('\n')
		if err != nil {
			return
		}
		var req helperReq
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		resp := helperResp{OK: true}
		ctx := context.Background()
		switch req.Cmd {
		case "add":
			s.back.AddTorrent(ctx, Torrent{Magnet: req.Magnet, DataDir: req.DataDir, Network: req.Network})
		case "pause":
			s.back.Pause(ctx)
		case "resume":
			s.back.Resume(ctx)
		case "uploaded":
			n, _ := s.back.UploadedBytes(ctx)
			resp.Uploaded = n
		case "peers":
			p, _ := s.back.Peers(ctx)
			resp.Peers = p
		case "close":
			s.back.Close(ctx)
		default:
			resp = helperResp{OK: false, Error: "unknown cmd"}
		}
		out, _ := json.Marshal(resp)
		rw.Write(append(out, '\n'))
		rw.Flush()
	}
}

func TestSocketHelperRoundTrip(t *testing.T) {
	srv := startFakeSocketServer(t)
	srv.back.setUploaded(4242)
	srv.back.peers = 7

	ctx := context.Background()
	h, err := DialSocketHelper(ctx, srv.path())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(ctx)

	if err := h.AddTorrent(ctx, Torrent{Magnet: "magnet:?x", DataDir: "/w", Network: "clearnet"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(srv.back.added) != 1 || srv.back.added[0].Network != "clearnet" {
		t.Fatalf("add not recorded: %+v", srv.back.added)
	}
	if err := h.Pause(ctx); err != nil || !srv.back.isPaused() {
		t.Fatalf("pause: err=%v paused=%v", err, srv.back.isPaused())
	}
	if err := h.Resume(ctx); err != nil || srv.back.isPaused() {
		t.Fatalf("resume: err=%v paused=%v", err, srv.back.isPaused())
	}
	up, err := h.UploadedBytes(ctx)
	if err != nil || up != 4242 {
		t.Fatalf("uploaded: %d err=%v", up, err)
	}
	peers, err := h.Peers(ctx)
	if err != nil || peers != 7 {
		t.Fatalf("peers: %d err=%v", peers, err)
	}
}
