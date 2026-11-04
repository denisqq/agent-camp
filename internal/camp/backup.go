package camp

import (
	"archive/tar"
	"github.com/denisqq/agent-camp/internal/objectstore"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Backup streams a set of pinned space roots and all reachable immutable objects.
// GC is deliberately absent in v1, so roots remain reachable throughout export.
func (s *Store) Backup(ctx context.Context, out io.Writer) error {
	spaces, err := s.Spaces(ctx)
	if err != nil {
		return err
	}
	roots := map[string]Snapshot{}
	for _, sp := range spaces {
		head, err := s.Load(ctx, sp.ID)
		if err != nil {
			return err
		}
		roots[sp.ID] = head
	}
	return s.backupRoots(ctx, out, roots)
}

func (s *Store) backupRoots(ctx context.Context, out io.Writer, roots map[string]Snapshot) error {
	tw := tar.NewWriter(out)
	seen := map[string]bool{}
	walked := map[string]bool{}
	write := func(key string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: key, Mode: 0600, Size: int64(len(data)), PAXRecords: map[string]string{"camp.sha256": digest(string(data))}}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	var visit func(string, string) error
	visit = func(key, segment string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		walkKey := key + "|" + segment
		if walked[walkKey] {
			return nil
		}
		walked[walkKey] = true
		data, _, err := s.Objects.Get(ctx, key)
		if err != nil {
			return err
		}
		if !seen[key] {
			if err = write(key, data); err != nil {
				return err
			}
			seen[key] = true
		}
		if strings.HasPrefix(key, "uploads/") {
			var h UploadHandle
			if err = json.Unmarshal(data, &h); err != nil {
				return err
			}
			if h.Root != "" {
				return visit(prefix(h.Space)+"file-nodes/"+h.Root, "")
			}
			return nil
		}
		parts := strings.Split(key, "/")
		if len(parts) < 4 {
			return nil
		}
		space := parts[1]
		kind := parts[2]
		switch kind {
		case "commits":
			var snap Snapshot
			if err = json.Unmarshal(data, &snap); err != nil {
				return err
			}
			if snap.Parent != "" {
				if err = visit(prefix(space)+"commits/"+snap.Parent, ""); err != nil {
					return err
				}
			}
			for _, root := range []string{snap.Records, snap.Paths, snap.Operations, snap.Control} {
				if root != "" {
					if err = visit(prefix(space)+"nodes/"+root, ""); err != nil {
						return err
					}
				}
			}
			for _, rev := range snap.Changes {
				if err = visit(prefix(space)+"revisions/"+rev, ""); err != nil {
					return err
				}
			}
		case "nodes", "file-nodes":
			var node struct {
				Left, Right string
				Value       json.RawMessage
			}
			if err = json.Unmarshal(data, &node); err != nil {
				return err
			}
			for _, child := range []string{node.Left, node.Right} {
				if child != "" {
					if err = visit(prefix(space)+kind+"/"+child, segment); err != nil {
						return err
					}
				}
			}
			if kind == "file-nodes" {
				var id string
				if err = json.Unmarshal(node.Value, &id); err != nil {
					return err
				}
				return visit(prefix(space)+"blobs/"+id, "")
			}
			var rec Record
			if json.Unmarshal(node.Value, &rec) == nil && rec.Revision != "" {
				return visit(prefix(space)+"revisions/"+rec.Revision, "")
			}
		case "revisions":
			var rev Revision
			if err = json.Unmarshal(data, &rev); err != nil {
				return err
			}
			if rev.Blob != "" {
				return visit("uploads/"+rev.Blob, "")
			}
		}
		return nil
	}
	for space, root := range roots {
		if err := visit(prefix(space)+"commits/"+root.Commit, ""); err != nil {
			return err
		}
	}
	// The final marker distinguishes a complete archive from an interrupted stream.
	marker, err := encode(struct {
		Version int                 `json:"version"`
		Roots   map[string]Snapshot `json:"roots"`
	}{1, roots})
	if err != nil {
		return err
	}
	if err = write("backup-complete.json", marker); err != nil {
		return err
	}
	return tw.Close()
}
func (s *Store) Restore(ctx context.Context, input io.Reader) error {
	existing, err := s.Spaces(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return errors.New("restore requires an empty installation")
	}
	tr := tar.NewReader(input)
	var roots map[string]Snapshot
	complete := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if complete {
			return errors.New("data after completion marker")
		}
		key := header.Name
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 16*1024*1024 || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
			return ErrInvalid
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if digest(string(data)) != header.PAXRecords["camp.sha256"] {
			return errors.New("backup checksum mismatch")
		}
		if key == "backup-complete.json" {
			var marker struct {
				Version int                 `json:"version"`
				Roots   map[string]Snapshot `json:"roots"`
			}
			if err = json.Unmarshal(data, &marker); err != nil {
				return err
			}
			if marker.Version != 1 {
				return ErrInvalid
			}
			roots = marker.Roots
			complete = true
			continue
		}
		if !strings.HasPrefix(key, "spaces/") && !strings.HasPrefix(key, "uploads/") {
			return ErrInvalid
		}
		if strings.HasSuffix(key, "/HEAD") {
			return ErrInvalid
		}
		if _, err = s.Objects.Put(ctx, key, data, "*"); errors.Is(err, objectstore.ErrConflict) {
			old, _, readErr := s.Objects.Get(ctx, key)
			if readErr != nil || string(old) != string(data) {
				return errors.New("restore object collision")
			}
		} else if err != nil {
			return err
		}
	}
	if !complete {
		return errors.New("incomplete backup")
	}
	for id, root := range roots {
		if !safeID.MatchString(id) || root.Space.ID != id {
			return ErrInvalid
		}
		stored, err := s.At(ctx, id, root.Commit)
		if err != nil {
			return err
		}
		expected, _ := encode(stored)
		supplied, _ := encode(root)
		if string(expected) != string(supplied) {
			return errors.New("backup root does not match accepted commit")
		}
	}
	// Validate every reachable revision, manifest and file chunk before publishing any root.
	if err = s.backupRoots(ctx, io.Discard, roots); err != nil {
		return err
	}
	for id, root := range roots {
		data, _ := encode(root)
		if _, err = s.Objects.Put(ctx, prefix(id)+"HEAD", data, "*"); err != nil {
			return err
		}
		if _, err = s.Objects.Put(ctx, "registry/spaces/"+id, []byte(id), "*"); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}
