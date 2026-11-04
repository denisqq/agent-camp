package camp

import (
	"github.com/datretyakov/agent-camp/internal/objectstore"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = objectstore.ErrNotFound
var ErrConflict = objectstore.ErrConflict
var ErrDenied = errors.New("access denied")
var ErrInvalid = errors.New("invalid request")

// Access model (Notion-like): permissions attach to sections of the path
// tree. A section record (kind=section) carries an Access override for its
// subtree; the deepest override wins and replaces everything above it.
// Roles map to actions: reader→read, commenter→read+propose, writer→all.
type Access struct {
	Visibility string       `json:"visibility,omitempty"` // open|closed
	Share      []ShareEntry `json:"share,omitempty"`
}
type ShareEntry struct {
	Subject string `json:"subject"`
	Role    string `json:"role"` // reader|commenter|writer
}
type Space struct {
	Access *Access `json:"access,omitempty"`
	ID     string  `json:"id"`
	Kind   string  `json:"kind"` // personal|team|shared|system
	Owner  string  `json:"owner"`
	Policy string  `json:"policy"`
}
type Record struct {
	ID       string         `json:"id"`
	Path     string         `json:"path"`
	Revision string         `json:"revision"`
	Kind     string         `json:"kind"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Deleted  bool           `json:"deleted,omitempty"`
}
type Revision struct {
	ID       string         `json:"id"`
	RecordID string         `json:"record_id"`
	Path     string         `json:"path"`
	Kind     string         `json:"kind"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Parent   string         `json:"parent,omitempty"`
	Actor    string         `json:"actor"`
	Time     time.Time      `json:"time"`
	Deleted  bool           `json:"deleted,omitempty"`
	Blob     string         `json:"blob,omitempty"`
	Size     int64          `json:"size,omitempty"`
	Sources  []SourceRef    `json:"sources,omitempty"`
}

// SourceRef is internal provenance, never included in public record metadata.
type SourceRef struct {
	Space    string `json:"space"`
	Record   string `json:"record"`
	Revision string `json:"revision"`
	Policy   string `json:"policy"`
	Path     string `json:"path"`
}
type Snapshot struct {
	Space      Space            `json:"space"`
	Commit     string           `json:"commit"`
	Parent     string           `json:"parent,omitempty"`
	Records    string           `json:"records"`
	Paths      string           `json:"paths"`
	Operations string           `json:"operations"`
	Control    string           `json:"control,omitempty"`
	Changes    []string         `json:"changes,omitempty"`
	Generation map[string]int64 `json:"generation,omitempty"`
	ETag       string           `json:"-"`
}
type Dependency struct {
	AllowDeleted bool   `json:"allow_deleted,omitempty"`
	Revision     string `json:"revision"`
	Space        string `json:"space"`
	Record       string `json:"record"`
	Path         string `json:"path"`
	Policy       string `json:"policy"`
}
type Receipt struct {
	Status       string       `json:"status"`
	Result       any          `json:"result,omitempty"`
	Stdout       string       `json:"stdout,omitempty"`
	ChangeID     string       `json:"change_id,omitempty"`
	Error        string       `json:"error,omitempty"`
	Fingerprint  string       `json:"fingerprint,omitempty"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func encode(v any) (json.RawMessage, error) { return json.Marshal(v) }
