package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Defaults for the two services the resolver reads.
const (
	DefaultPirateFaceBase  = "https://pirateface.co"
	DefaultHFEndpoint      = "https://huggingface.co"
	DefaultHFRevision      = "main"
	maxTreePages           = 100
	maxResponseBodyBytes   = 64 << 20
	defaultResolverTimeout = 30 * time.Second
)

// ErrNotListed means PirateFace has no torrent for the requested repo.
var ErrNotListed = errors.New("model is not listed on PirateFace")

// PirateFaceResolver builds a model's distribution record from two real
// sources, each matching an official client's code:
//
//  1. The magnet comes from PirateFace: GET {base}/api/torrents?repo=<id>,
//     JSON object, field "magnet"; 404 means not listed. This is exactly what
//     the official `pirateface` client does (pirateface/client/api.py). No other
//     field of that response is relied on.
//
//  2. The per-file fingerprints come from Hugging Face at a pinned revision:
//     GET {hf}/api/models/<id>/tree/<url-escaped revision>?recursive=true,
//     paginated by a `Link: <...>; rel="next"` header. Each entry has `type`
//     ("file"/"directory"), `path`, `size`, `oid` (git blob sha1) and, for
//     LFS files, `lfs.oid` (sha256). This matches huggingface_hub's
//     list_repo_tree / RepoFile parsing. PirateFace's own client verifies
//     torrent files against these same fingerprints.
//
// Pin Revision to the commit shown on the model's PirateFace page: the torrent
// was built from that revision, so its files hash to that tree. A moving
// revision like "main" fails verification closed if upstream changes.
type PirateFaceResolver struct {
	Base       string       // PirateFace base URL; empty = DefaultPirateFaceBase
	HFEndpoint string       // Hugging Face endpoint; empty = DefaultHFEndpoint
	Revision   string       // HF revision; empty = DefaultHFRevision
	HTTP       *http.Client // injected in tests
}

func (p PirateFaceResolver) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: defaultResolverTimeout}
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return strings.TrimRight(v, "/")
}

// Resolve fetches the magnet and the file fingerprints for repo id.
func (p PirateFaceResolver) Resolve(id string) (Resolved, error) {
	magnet, magnetURL, err := p.lookupMagnet(id)
	if err != nil {
		return Resolved{}, err
	}
	rev := orDefault(p.Revision, DefaultHFRevision)
	files, treeURL, err := p.listTree(id, rev)
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{
		ID:       id,
		Magnet:   magnet,
		Files:    files,
		Revision: rev,
		Origin:   "magnet:" + magnetURL + " files:" + treeURL,
	}
	if err := r.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("record for %q did not validate: %w", id, err)
	}
	return r, nil
}

func (p PirateFaceResolver) lookupMagnet(id string) (magnet, endpoint string, err error) {
	endpoint = orDefault(p.Base, DefaultPirateFaceBase) + "/api/torrents?" + url.Values{"repo": {id}}.Encode()
	body, status, _, err := p.get(endpoint)
	if err != nil {
		return "", endpoint, fmt.Errorf("pirateface lookup: %w", err)
	}
	if status == http.StatusNotFound {
		return "", endpoint, fmt.Errorf("%w: %s", ErrNotListed, id)
	}
	if status != http.StatusOK {
		return "", endpoint, fmt.Errorf("pirateface lookup %s: HTTP %d", id, status)
	}
	var entry struct {
		Magnet string `json:"magnet"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		return "", endpoint, fmt.Errorf("pirateface lookup %s: decode: %w", id, err)
	}
	if !strings.HasPrefix(entry.Magnet, "magnet:?") {
		// The official client treats a missing magnet as "unknown", e.g. a
		// license that must be accepted first.
		return "", endpoint, fmt.Errorf("pirateface has no usable magnet for %s", id)
	}
	return entry.Magnet, endpoint, nil
}

// hfTreeEntry mirrors the fields huggingface_hub reads from the tree API.
type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	OID  string `json:"oid"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

func (p PirateFaceResolver) listTree(id, rev string) ([]FileChecksum, string, error) {
	hf := orDefault(p.HFEndpoint, DefaultHFEndpoint)
	// huggingface_hub: f"{endpoint}/api/{repo_type}s/{repo_id}/tree/{quote(revision, safe='')}"
	first := fmt.Sprintf("%s/api/models/%s/tree/%s?recursive=true", hf, id, url.PathEscape(rev))
	var files []FileChecksum
	next := first
	for page := 0; next != ""; page++ {
		if page >= maxTreePages {
			return nil, first, fmt.Errorf("hugging face tree for %s exceeded %d pages", id, maxTreePages)
		}
		body, status, hdr, err := p.get(next)
		if err != nil {
			return nil, first, fmt.Errorf("hugging face tree: %w", err)
		}
		if status != http.StatusOK {
			return nil, first, fmt.Errorf("hugging face tree %s@%s: HTTP %d", id, rev, status)
		}
		var entries []hfTreeEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, first, fmt.Errorf("hugging face tree %s: decode: %w", id, err)
		}
		for _, e := range entries {
			if e.Type != "file" {
				continue
			}
			fc := FileChecksum{Path: e.Path, Size: e.Size}
			if e.LFS != nil {
				fc.Algo, fc.Hex, fc.Size = SHA256, strings.ToLower(e.LFS.OID), e.LFS.Size
			} else {
				fc.Algo, fc.Hex = GitBlobSHA1, strings.ToLower(e.OID)
			}
			files = append(files, fc)
		}
		next = nextLink(hdr.Get("Link"))
	}
	return files, first, nil
}

// nextLink extracts the rel="next" URL from a GitHub-style Link header.
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 {
			continue
		}
		u := strings.TrimSpace(seg[0])
		if !strings.HasPrefix(u, "<") || !strings.HasSuffix(u, ">") {
			continue
		}
		for _, attr := range seg[1:] {
			if strings.ReplaceAll(strings.TrimSpace(attr), " ", "") == `rel="next"` {
				return u[1 : len(u)-1]
			}
		}
	}
	return ""
}

func (p PirateFaceResolver) get(u string) ([]byte, int, http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	return body, resp.StatusCode, resp.Header, err
}
