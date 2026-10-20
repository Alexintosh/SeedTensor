package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultPirateFaceBase is the public PirateFace base URL.
//
// NOTE ON STABILITY: as of this writing PirateFace publishes model pages with
// magnet links and SHA-256 checksum records, runs its own tracker, and lists a
// planned drop-in API (an HF_ENDPOINT-style endpoint) marked "soon". The exact
// JSON schema of that API is not yet documented. This resolver therefore codes
// against an ASSUMED response shape (pfModelResponse below) and keeps all of
// that assumption in this one file. When the real API lands, adjust
// pfModelResponse and parsePF to match; nothing else in the codebase needs to
// change, because everything downstream consumes the neutral Resolved struct.
const DefaultPirateFaceBase = "https://pirateface.co"

// PirateFaceResolver resolves a model via the PirateFace registry.
type PirateFaceResolver struct {
	// Base overrides the registry base URL. Empty uses DefaultPirateFaceBase.
	Base string
	// HTTP is the client used for requests. Empty uses a default with a
	// sane timeout. Injected in tests to serve a canned response.
	HTTP *http.Client
}

// pfModelResponse is the ASSUMED PirateFace model-metadata JSON. See the note
// on DefaultPirateFaceBase. Fields are best-effort guesses at the real schema.
type pfModelResponse struct {
	ID       string `json:"id"`
	Magnet   string `json:"magnet"`
	InfoHash string `json:"info_hash_v2"`
	Revision string `json:"revision"`
	Files    []struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func (p PirateFaceResolver) base() string {
	if p.Base != "" {
		return strings.TrimRight(p.Base, "/")
	}
	return DefaultPirateFaceBase
}

func (p PirateFaceResolver) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// endpoint builds the metadata URL for a model id. The id may contain a slash
// (e.g. "TensorFold/Qwen3.8-27B-oQ4"); it is path-escaped as a single segment.
func (p PirateFaceResolver) endpoint(id string) string {
	return fmt.Sprintf("%s/api/v1/model/%s", p.base(), url.PathEscape(id))
}

func (p PirateFaceResolver) Resolve(id string) (Resolved, error) {
	ep := p.endpoint(id)
	req, err := http.NewRequest(http.MethodGet, ep, nil)
	if err != nil {
		return Resolved{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client().Do(req)
	if err != nil {
		return Resolved{}, fmt.Errorf("pirateface request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Resolved{}, fmt.Errorf("pirateface returned %s for %s: %s", resp.Status, id, strings.TrimSpace(string(body)))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Resolved{}, fmt.Errorf("read pirateface response: %w", err)
	}

	return parsePF(raw, id, ep)
}

// parsePF turns the assumed PirateFace JSON into a neutral Resolved record.
// Split out so tests can exercise it without HTTP.
func parsePF(raw []byte, id, origin string) (Resolved, error) {
	var pf pfModelResponse
	if err := json.Unmarshal(raw, &pf); err != nil {
		return Resolved{}, fmt.Errorf("parse pirateface JSON: %w", err)
	}
	r := Resolved{
		ID:         id,
		Magnet:     pf.Magnet,
		InfoHashV2: pf.InfoHash,
		Revision:   pf.Revision,
		Origin:     "pirateface:" + origin,
	}
	for _, f := range pf.Files {
		r.Files = append(r.Files, FileChecksum{
			Path: f.Path,
			Size: f.Size,
			Algo: SHA256,
			Hex:  strings.ToLower(f.SHA256),
		})
	}
	if err := r.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("pirateface record for %q did not validate: %w", id, err)
	}
	return r, nil
}
