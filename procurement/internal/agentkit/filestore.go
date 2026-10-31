package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// FileStore is a task store that keeps every task as JSON in a directory, so an agent
// that restarts still knows the tasks it was working on (an approval it is waiting for,
// say). It serves from memory and writes each task through to <dir>/<id>.json. One
// process per directory.
type FileStore struct {
	*taskstore.InMemory
	dir string
	mu  sync.Mutex
}

var _ taskstore.Store = (*FileStore)(nil)

// NewFileStore loads the tasks saved in dir (created if missing) and returns the store.
func NewFileStore(ctx context.Context, dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &FileStore{InMemory: taskstore.NewInMemory(nil), dir: dir}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var t a2a.Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("agentkit: %s: %w", f, err)
		}
		if _, err := s.InMemory.Create(ctx, &t); err != nil {
			return nil, fmt.Errorf("agentkit: %s: %w", f, err)
		}
	}
	return s, nil
}

// Create implements taskstore.Store.
func (s *FileStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.InMemory.Create(ctx, task)
	if err != nil {
		return v, err
	}
	return v, s.save(ctx, task.ID)
}

// Update implements taskstore.Store.
func (s *FileStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.InMemory.Update(ctx, req)
	if err != nil {
		return v, err
	}
	return v, s.save(ctx, req.Task.ID)
}

// save writes the stored task to its file, atomically.
func (s *FileStore) save(ctx context.Context, id a2a.TaskID) error {
	st, err := s.InMemory.Get(ctx, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(st.Task)
	if err != nil {
		return err
	}
	name := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(string(id))
	tmp, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, name+".json"))
}
