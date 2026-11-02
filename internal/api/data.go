package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// The server is the main store for each player's data. Each kind (collection, characters, exalts, pets,
// dps_history, ...) is one JSON document with a revision number:
//
//  1. On connect the client asks what the server has: GET /api/v1/me/data?meta=true (revisions only), then the
//     documents it needs.
//  2. It uploads a change with the revision it last saw: PUT /api/v1/me/data/{kind} {"baseRevision": n, "data": ...}.
//  3. If someone else (another PC) wrote in between, the server answers 409 with the current document, and the
//     client merges and retries.

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

const maxKindsPerUser = 32

type dataResponse struct {
	Seq   int64                     `json:"seq"` // newest change on the server, for catching up on live updates later
	Items map[string]store.DataItem `json:"items"`
}

func (s *Server) getData(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.principal(r)
	if err != nil {
		return err
	}
	var kinds []string
	if param := r.URL.Query().Get("kinds"); param != "" {
		kinds = []string{}
		for _, k := range strings.Split(param, ",") {
			if k = strings.TrimSpace(k); kindPattern.MatchString(k) {
				kinds = append(kinds, k)
			}
		}
	}
	withData := r.URL.Query().Get("meta") != "true"

	ctx := r.Context()
	seq, err := store.LatestSeq(ctx, s.store.DB())
	if err != nil {
		return err
	}
	items, err := store.ListData(ctx, s.store.DB(), user.ID, kinds, withData)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, dataResponse{Seq: seq, Items: items})

	var size int
	var summary []string
	for kind, item := range items {
		size += len(item.Data)
		summary = append(summary, kind+"@"+strconv.FormatInt(item.Revision, 10))
	}
	sort.Strings(summary)
	if len(summary) == 0 {
		summary = []string{"none"}
	}
	what := "data"
	if !withData {
		what = "revisions only"
	}
	s.log.Info("sync download", "ign", user.IGN, "what", what, "kinds", strings.Join(summary, ","), "size", formatSize(size))
	return nil
}

// formatSize shows a byte count the way people read it (812 B, 14.2 KiB, 1.3 MiB).
func formatSize(n int) string {
	switch {
	case n < 1024:
		return strconv.Itoa(n) + " B"
	case n < 1024*1024:
		return strconv.FormatFloat(float64(n)/1024, 'f', 1, 64) + " KiB"
	default:
		return strconv.FormatFloat(float64(n)/(1024*1024), 'f', 1, 64) + " MiB"
	}
}

type putDataResponse struct {
	Kind      string `json:"kind"`
	Revision  int64  `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Seq       int64  `json:"seq"`
}

func (s *Server) putData(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.principal(r)
	if err != nil {
		return err
	}
	kind := r.PathValue("kind")
	if !kindPattern.MatchString(kind) {
		return errBadRequest("kind must be lower case letters, digits or _, at most 32 characters")
	}
	var req struct {
		BaseRevision *int64          `json:"baseRevision"` // 0 when creating
		Data         json.RawMessage `json:"data"`
	}
	if err := decodeJSON(w, r, dataBody, &req, false); err != nil {
		return err
	}
	if req.BaseRevision == nil {
		return errBadRequest("baseRevision is required (0 for a new document)")
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		return errBadRequest("data is required")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, req.Data); err != nil {
		return errBadRequest("data is not valid JSON")
	}

	ctx := r.Context()
	now := s.nowMs()
	resp := putDataResponse{Kind: kind, Revision: *req.BaseRevision + 1, UpdatedAt: now}
	err = s.store.Tx(ctx, func(q store.Querier) error {
		current, err := store.GetData(ctx, q, user.ID, kind)
		if err != nil {
			return err
		}
		var currentRev int64
		if current != nil {
			currentRev = current.Revision
		}
		if currentRev != *req.BaseRevision {
			s.log.Info("sync conflict, client will merge and retry", "ign", user.IGN, "kind", kind,
				"clientRevision", *req.BaseRevision, "serverRevision", currentRev)
			return &apiError{Status: http.StatusConflict, Code: "revision_conflict",
				Message: "The server has a newer version of this data", Current: current}
		}
		if current == nil {
			n, err := store.CountData(ctx, q, user.ID)
			if err != nil {
				return err
			}
			if n >= maxKindsPerUser {
				return errBadRequest("Too many data kinds for one account")
			}
		}
		resp.Seq, err = store.PutData(ctx, q, user.ID, kind, resp.Revision, compact.Bytes(), now)
		if err == nil && kind == "collection" {
			_, err = store.RecordDrops(ctx, q, user.ID, compact.Bytes()) // kept even if the collection is cleared later
		}
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	s.log.Info("sync upload", "ign", user.IGN, "kind", kind, "revision", resp.Revision, "size", formatSize(compact.Len()))
	return nil
}
