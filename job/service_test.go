package job

import (
	"errors"
	"testing"

	sql "github.com/FloatTech/sqlite"
)

type fakeTaskRepository struct {
	rows map[string]storedJob
}

func (f *fakeTaskRepository) initSchema() error { return nil }
func (f *fakeTaskRepository) save(botID int64, task storedJob) error {
	f.rows[jobKey(botID, task.ID)] = task
	return nil
}
func (f *fakeTaskRepository) list(botID int64) ([]storedJob, error) {
	return nil, nil
}
func (f *fakeTaskRepository) find(botID, taskID int64) (storedJob, error) {
	task, ok := f.rows[jobKey(botID, taskID)]
	if !ok {
		return storedJob{}, sql.ErrNullResult
	}
	return task, nil
}
func (f *fakeTaskRepository) delete(botID int64, taskIDs []int64) error {
	for _, taskID := range taskIDs {
		delete(f.rows, jobKey(botID, taskID))
	}
	return nil
}

func TestTaskServiceRejectsBatchConflictBeforeWriting(t *testing.T) {
	store := &fakeTaskRepository{rows: make(map[string]storedJob)}
	service := taskService{store: store, runtime: newRuntimeRegistry()}
	existing := storedJob{ID: 1, Kind: storedFullMatch, Matcher: "hello", Command: `"world"`}
	if err := store.save(100, existing); err != nil {
		t.Fatal(err)
	}
	newTask := storedJob{ID: 2, Kind: storedFullMatch, Matcher: "bye", Command: `"world"`}
	if err := service.addBatch(100, []storedJob{newTask, existing}); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("addBatch() error = %v", err)
	}
	if _, ok := store.rows[jobKey(100, newTask.ID)]; ok {
		t.Fatal("batch conflict wrote a partial task")
	}
}
