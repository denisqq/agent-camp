package camp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

const mapCursorLifetime = 30 * time.Minute

var invalidMapCursor = fmt.Errorf("%w: invalid map cursor; restart map without cursor", ErrInvalid)

type MapCursor struct {
	Version      int    `json:"v"`
	TokenHash    string `json:"token"`
	Subject      string `json:"subject"`
	Space        string `json:"space,omitempty"`
	Path         string `json:"path,omitempty"`
	View         string `json:"view,omitempty"`
	Depth        int    `json:"depth,omitempty"`
	BudgetBytes  int    `json:"budget,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"`
	ScanStart    string `json:"after,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	WindowDigest string `json:"visibility,omitempty"`
	Expires      int64  `json:"expires"`
}

type mapCursorKeyState struct {
	once sync.Once
	key  []byte
	err  error
}

func (b *Broker) cursorState() *mapCursorKeyState {
	b.mapCursorMu.Lock()
	defer b.mapCursorMu.Unlock()
	if b.mapCursors == nil {
		b.mapCursors = &mapCursorKeyState{}
	}
	return b.mapCursors
}
func (b *Broker) mapCursorKey() ([]byte, error) {
	state := b.cursorState()
	state.once.Do(func() { state.key = make([]byte, 32); _, state.err = io.ReadFull(rand.Reader, state.key) })
	return state.key, state.err
}
func mapCursorCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealMapCursor(key []byte, c MapCursor) (string, error) {
	plaintext, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	aead, err := mapCursorCipher(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte("camp.map.v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func openMapCursor(key []byte, token string) (MapCursor, error) {
	var c MapCursor
	if len(token) > 16384 {
		return c, invalidMapCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return c, invalidMapCursor
	}
	aead, err := mapCursorCipher(key)
	if err != nil {
		return c, err
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return c, invalidMapCursor
	}
	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("camp.map.v1"))
	if err != nil {
		return c, invalidMapCursor
	}
	if err = json.Unmarshal(plaintext, &c); err != nil || c.Version != 1 || c.Expires <= time.Now().Unix() || c.Offset < 0 {
		return MapCursor{}, invalidMapCursor
	}
	return c, nil
}
func (c MapCursor) matches(p Principal, space, root string, opts NavigationOptions) bool {
	return c.TokenHash == p.TokenHash && c.Subject == p.Subject && c.Space == space && c.Path == root && c.View == opts.View && c.Depth == opts.Depth && c.BudgetBytes == opts.BudgetBytes
}
