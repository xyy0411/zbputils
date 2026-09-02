package job

import (
	"path/filepath"
	"testing"
	"time"

	sql "github.com/FloatTech/sqlite"
)

func TestRepositorySeparatesBotsWithSameTaskID(t *testing.T) {
	database := sql.New(filepath.Join(t.TempDir(), "job.db"))
	if err := database.Open(time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	repo := repository{db: &database}
	if err := repo.initSchema(); err != nil {
		t.Fatal(err)
	}
	task := storedJob{ID: 7, Kind: storedFullMatch, Matcher: "hello", Command: `"world"`}
	if err := repo.save(1, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.save(2, task); err != nil {
		t.Fatal(err)
	}

	for _, botID := range []int64{1, 2} {
		tasks, err := repo.list(botID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 1 || tasks[0].ID != 7 {
			t.Fatalf("repo.list(%d) = %#v", botID, tasks)
		}
	}
}
