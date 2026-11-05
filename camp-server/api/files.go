package api

import (
	"github.com/denisqq/agent-camp/camp-server/camp"
	"net/http"
	"strconv"
)

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	if r.ContentLength < 0 {
		reply(w, nil, camp.ErrInvalid)
		return
	}
	space := r.URL.Query().Get("space")
	path := r.URL.Query().Get("path")
	h, err := a.Sessions.Broker.Upload(r.Context(), p, space, path, r.Header.Get("Content-Type"), r.ContentLength, r.Header.Get("X-Content-SHA256"), r.Body)
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]any{"handle": h.ID, "size": h.Size, "sha256": h.SHA256, "expires": h.Expires}, nil)
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	p, err := principal(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	space := r.URL.Query().Get("space")
	path := r.URL.Query().Get("path")
	snap, err := a.Sessions.Broker.Store.Load(r.Context(), space)
	if err != nil {
		reply(w, nil, camp.ErrDenied)
		return
	}
	e := &camp.Execution{Principal: p, Primary: space, Snapshots: map[string]camp.Snapshot{space: snap}, Staged: map[string]camp.Revision{}}
	rec, err := a.Sessions.Broker.Call(r.Context(), e, "stat", map[string]any{"path": path})
	if err != nil {
		reply(w, nil, err)
		return
	}
	record := rec.(camp.Record)
	rev, err := a.Sessions.Broker.Store.ReadRevision(r.Context(), space, record.Revision)
	if err != nil {
		reply(w, nil, err)
		return
	}
	size := rev.Size
	if rev.Blob == "" {
		size = int64(len(rev.Content))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	for offset := int64(0); offset < size; {
		chunk, err := a.Sessions.Broker.ReadFileChunk(r.Context(), e, space, path, offset, 65536)
		if err != nil {
			return
		}
		if len(chunk) == 0 {
			return
		}
		if _, err = w.Write(chunk); err != nil {
			return
		}
		offset += int64(len(chunk))
	}
}
