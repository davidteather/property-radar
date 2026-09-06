package photostore

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"sync"
)

// Memory is an in-process Store backend; it passes the same conformance suite
// as Local and S3, and needs neither a disk nor a bucket.
type Memory struct {
	mu      sync.Mutex
	objects map[string]memObject
}

type memObject struct {
	data          []byte
	width, height int
}

var _ Store = (*Memory)(nil)

func NewMemory() *Memory {
	return &Memory{objects: map[string]memObject{}}
}

func (m *Memory) Stat(_ context.Context, key string) (Info, error) {
	if _, err := cleanKey(key); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return Info{}, nil
	}
	return Info{Exists: true, Width: obj.width, Height: obj.height}, nil
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	if _, err := cleanKey(key); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("photostore: get %s: %w", key, os.ErrNotExist)
	}
	return bytes.Clone(obj.data), nil
}

// Delete drops the object, treating an already-absent key as success.
func (m *Memory) Delete(_ context.Context, key string) error {
	if _, err := cleanKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *Memory) Put(_ context.Context, key string, data []byte, width, height int) error {
	if _, err := cleanKey(key); err != nil {
		return err
	}
	if width == 0 || height == 0 {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			width, height = cfg.Width, cfg.Height
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{data: bytes.Clone(data), width: width, height: height}
	return nil
}
