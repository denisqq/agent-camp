package camp

import (
	"github.com/datretyakov/agent-camp/internal/tree"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const FileChunk = 1024 * 1024

type UploadHandle struct {
	ID      string    `json:"id"`
	Space   string    `json:"space"`
	Path    string    `json:"path"`
	Owner   string    `json:"owner"`
	Root    string    `json:"root"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	MIME    string    `json:"mime"`
	Expires time.Time `json:"expires"`
}

func (b *Broker) Upload(ctx context.Context, p Principal, space, path, mime string, size int64, expected string, input io.Reader) (UploadHandle, error) {
	var h UploadHandle
	p, err := b.Auth.Current(ctx, p)
	if err != nil {
		return h, err
	}
	snap, err := b.Store.Load(ctx, space)
	if err != nil {
		return h, ErrDenied
	}
	if err = b.AuthorizePath(ctx, p, snap, path, "write"); err != nil {
		return h, err
	}
	if size < 0 || size > 100*1024*1024*1024 {
		return h, ErrInvalid
	}
	h = UploadHandle{ID: ID(), Space: space, Path: path, Owner: p.TokenHash, Size: size, MIME: mime, Expires: time.Now().Add(time.Hour)}
	tr := tree.New(b.Store.Objects, prefix(space)+"file-nodes/")
	hash := sha256.New()
	buf := make([]byte, FileChunk)
	var read int64
	for read < size {
		n, err := io.ReadFull(input, buf[:min(int64(len(buf)), size-read)])
		if err != nil {
			return UploadHandle{}, err
		}
		hash.Write(buf[:n])
		sum := sha256.Sum256(buf[:n])
		id := hex.EncodeToString(sum[:])
		key := prefix(space) + "blobs/" + id
		if _, err = b.Store.Objects.Put(ctx, key, buf[:n], "*"); err != nil && !errors.Is(err, ErrConflict) {
			return UploadHandle{}, err
		}
		value, _ := encode(id)
		h.Root, err = tr.Set(ctx, h.Root, fmt.Sprintf("%020d", read/FileChunk), value)
		if err != nil {
			return UploadHandle{}, err
		}
		read += int64(n)
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || err != io.EOF {
		return UploadHandle{}, errors.New("upload length mismatch")
	}
	h.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if expected != "" && expected != h.SHA256 {
		return UploadHandle{}, errors.New("upload hash mismatch")
	}
	latest, err := b.Store.Load(ctx, space)
	if err != nil {
		return UploadHandle{}, err
	}
	if err = b.AuthorizePath(ctx, p, latest, path, "write"); err != nil {
		return UploadHandle{}, err
	}
	data, _ := encode(h)
	_, err = b.Store.Objects.Put(ctx, "uploads/"+h.ID, data, "*")
	return h, err
}
func (b *Broker) attach(ctx context.Context, e *Execution, p, handle string) (any, error) {
	if !safeID.MatchString(handle) {
		return nil, ErrInvalid
	}
	data, _, err := b.Store.Objects.Get(ctx, "uploads/"+handle)
	if err != nil {
		return nil, ErrDenied
	}
	var h UploadHandle
	if err = json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	if h.Path != p || h.Owner != e.Principal.TokenHash || h.Space != e.Primary || time.Now().After(h.Expires) {
		return nil, ErrDenied
	}
	rev := Revision{RecordID: ID(), Path: p, Kind: "artifact", Blob: h.ID, Size: h.Size, Metadata: map[string]any{"sha256": h.SHA256, "mime": h.MIME, "extraction": "unsupported"}}
	old, _, err := b.resolve(ctx, e, e.Primary, p, "")
	if err == nil {
		// Replacing an existing record requires write access to its current
		// path; a new upload handle alone grants no record access.
		if err = b.allow(ctx, e, e.Primary, old.Path, "write"); err != nil {
			return nil, err
		}
		rev.RecordID = old.ID
		rev.Parent = old.Revision
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err = b.stage(ctx, e, rev); err != nil {
		return nil, err
	}
	return map[string]any{"id": rev.RecordID, "path": p, "size": h.Size, "staged": true}, nil
}
func (b *Broker) ReadFileChunk(ctx context.Context, e *Execution, space, p string, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 1 || length > 65536 {
		return nil, ErrInvalid
	}
	_, rev, err := b.resolve(ctx, e, space, p, "")
	if err != nil {
		return nil, err
	}
	if rev.Blob == "" {
		data := []byte(rev.Content)
		if offset >= int64(len(data)) {
			return []byte{}, nil
		}
		return data[offset:min(int64(len(data)), offset+int64(length))], nil
	}
	if offset >= rev.Size {
		return []byte{}, nil
	}
	data, _, err := b.Store.Objects.Get(ctx, "uploads/"+rev.Blob)
	if err != nil {
		return nil, err
	}
	var h UploadHandle
	if err = json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	if h.Space != space {
		return nil, ErrDenied
	}
	tr := tree.New(b.Store.Objects, prefix(space)+"file-nodes/")
	out := make([]byte, 0, length)
	end := min(rev.Size, offset+int64(length))
	for offset < end {
		raw, err := tr.Get(ctx, h.Root, fmt.Sprintf("%020d", offset/FileChunk))
		if err != nil {
			return nil, err
		}
		var id string
		if err = json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		chunk, _, err := b.Store.Objects.Get(ctx, prefix(space)+"blobs/"+id)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(chunk)
		if hex.EncodeToString(sum[:]) != id {
			return nil, errors.New("corrupt file chunk")
		}
		start := offset % FileChunk
		n := min(int64(len(chunk))-start, end-offset)
		if n <= 0 {
			return nil, errors.New("invalid chunk manifest")
		}
		out = append(out, chunk[start:start+n]...)
		offset += n
	}
	return out, nil
}
func (b *Broker) fileCall(ctx context.Context, e *Execution, space, method, p string, a map[string]any) (any, error) {
	switch method {
	case "files.attach":
		if space != e.Primary {
			return nil, ErrDenied
		}
		return b.attach(ctx, e, p, str(a, "handle"))
	case "files.read_chunk":
		data, err := b.ReadFileChunk(ctx, e, space, p, int64(number(a, "offset", 0)), number(a, "size", 65536))
		return map[string]any{"base64": base64.StdEncoding.EncodeToString(data)}, err
	default:
		return nil, ErrInvalid
	}
}
