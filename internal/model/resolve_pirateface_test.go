package model

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fake servers below reproduce the response shapes of the real services as
// read by their official clients:
//   - PirateFace /api/torrents?repo=  (pirateface/client/api.py: reads "magnet",
//     404 = not listed)
//   - Hugging Face /api/models/<id>/tree/<rev>?recursive=true
//     (huggingface_hub list_repo_tree / RepoFile: type, path, size, oid, lfs.oid)

const testRepo = "Qwen/Qwen3.5-2B"
const testRev = "15852e8c16360a2fea060d615a32b45270f8a8fc"

var (
	lfsSHA   = strings.Repeat("ab", 32) // 64 hex
	blobSHA1 = strings.Repeat("cd", 20) // 40 hex
)

func newFakeServices(t *testing.T, listed bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var base string

	mux.HandleFunc("/api/torrents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("repo") != testRepo || !listed {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		io.WriteString(w, `{"magnet":"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Qwen3.5-2B"}`)
	})

	// Page 1 carries a Link header to page 2, as HF paginates.
	mux.HandleFunc("/api/models/"+testRepo+"/tree/"+testRev, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recursive") != "true" {
			http.Error(w, "expected recursive=true", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/models/%s/tree/%s?recursive=true&cursor=p2>; rel="next"`, base, testRepo, testRev))
			fmt.Fprintf(w, `[
				{"type":"file","oid":"%s","size":1234,"path":"config.json"},
				{"type":"directory","oid":"%s","size":0,"path":"tokenizer"},
				{"type":"file","oid":"%s","size":4500000000,"lfs":{"oid":"%s","size":4500000000,"pointerSize":135},"path":"model.safetensors"}
			]`, blobSHA1, blobSHA1, blobSHA1, lfsSHA)
			return
		}
		fmt.Fprintf(w, `[{"type":"file","oid":"%s","size":77,"path":"tokenizer/vocab.json"}]`, blobSHA1)
	})

	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func TestPirateFaceResolverRealShapes(t *testing.T) {
	srv := newFakeServices(t, true)
	r, err := PirateFaceResolver{Base: srv.URL, HFEndpoint: srv.URL, Revision: testRev, HTTP: srv.Client()}.Resolve(testRepo)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:") {
		t.Errorf("magnet = %q", r.Magnet)
	}
	if r.Revision != testRev {
		t.Errorf("revision = %q", r.Revision)
	}
	got := map[string]FileChecksum{}
	for _, f := range r.Files {
		got[f.Path] = f
	}
	if len(got) != 3 {
		t.Fatalf("want 3 files (directory skipped, page 2 followed), got %d: %+v", len(got), r.Files)
	}
	if f := got["model.safetensors"]; f.Algo != SHA256 || f.Hex != lfsSHA || f.Size != 4500000000 {
		t.Errorf("LFS file mapped wrong: %+v", f)
	}
	if f := got["config.json"]; f.Algo != GitBlobSHA1 || f.Hex != blobSHA1 {
		t.Errorf("non-LFS file mapped wrong: %+v", f)
	}
	if _, ok := got["tokenizer/vocab.json"]; !ok {
		t.Error("second page not followed")
	}
}

func TestPirateFaceResolverNotListed(t *testing.T) {
	srv := newFakeServices(t, false)
	_, err := PirateFaceResolver{Base: srv.URL, HFEndpoint: srv.URL, Revision: testRev, HTTP: srv.Client()}.Resolve(testRepo)
	if !errors.Is(err, ErrNotListed) {
		t.Fatalf("want ErrNotListed, got %v", err)
	}
}

func TestPirateFaceResolverUnknownRevision(t *testing.T) {
	srv := newFakeServices(t, true)
	_, err := PirateFaceResolver{Base: srv.URL, HFEndpoint: srv.URL, Revision: "deadbeef", HTTP: srv.Client()}.Resolve(testRepo)
	if err == nil {
		t.Fatal("expected error for a revision with no tree")
	}
}

func TestNextLink(t *testing.T) {
	cases := map[string]string{
		`<https://huggingface.co/api/models/a/tree/main?cursor=x>; rel="next"`: "https://huggingface.co/api/models/a/tree/main?cursor=x",
		`<https://x/prev>; rel="prev", <https://x/next>; rel="next"`:           "https://x/next",
		``:                             "",
		`<https://x/last>; rel="last"`: "",
	}
	for in, want := range cases {
		if got := nextLink(in); got != want {
			t.Errorf("nextLink(%q) = %q, want %q", in, got, want)
		}
	}
}
