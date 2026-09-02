package job

import (
	"errors"
	"strconv"

	sql "github.com/FloatTech/sqlite"
)

const jobTable = "job_records"

type jobRow struct {
	Key      string `db:"key"`
	TaskID   int64  `db:"task_id"`
	BotID    int64  `db:"bot_id"`
	Kind     string `db:"kind"`
	Matcher  string `db:"matcher"`
	Schedule string `db:"schedule"`
	Alias    string `db:"alias"`
	Command  string `db:"command"`
	GroupID  int64  `db:"group_id"`
	UserID   int64  `db:"user_id"`
}

type repository struct {
	db *sql.Sqlite
}

type taskRepository interface {
	initSchema() error
	save(botID int64, task storedJob) error
	list(botID int64) ([]storedJob, error)
	find(botID, taskID int64) (storedJob, error)
	delete(botID int64, taskIDs []int64) error
}

func (r repository) initSchema() error {
	return r.db.Create(jobTable, &jobRow{})
}

func (r repository) save(botID int64, task storedJob) error {
	row, err := newJobRow(botID, task)
	if err != nil {
		return err
	}
	return r.db.Insert(jobTable, &row)
}

func (r repository) list(botID int64) ([]storedJob, error) {
	rows := make([]storedJob, 0, 16)
	var row jobRow
	err := r.db.FindFor(jobTable, &row, "WHERE bot_id = ?", func() error {
		task, err := row.storedJob()
		if err != nil {
			return err
		}
		rows = append(rows, task)
		return nil
	}, botID)
	if errors.Is(err, sql.ErrNullResult) {
		return rows, nil
	}
	return rows, err
}

func (r repository) find(botID, taskID int64) (storedJob, error) {
	var row jobRow
	err := r.db.FindFor(jobTable, &row, "WHERE key = ?", func() error { return nil }, jobKey(botID, taskID))
	if err != nil {
		return storedJob{}, err
	}
	return row.storedJob()
}

func (r repository) delete(botID int64, taskIDs []int64) error {
	if len(taskIDs) == 0 {
		return nil
	}
	keys := make([]string, len(taskIDs))
	for i, taskID := range taskIDs {
		keys[i] = jobKey(botID, taskID)
	}
	query, args := sql.QuerySet("WHERE key", "IN", keys)
	return r.db.Del(jobTable, query, args...)
}

func newJobRow(botID int64, task storedJob) (jobRow, error) {
	kind, err := task.Kind.databaseName()
	if err != nil {
		return jobRow{}, err
	}
	return jobRow{
		Key:      jobKey(botID, task.ID),
		TaskID:   task.ID,
		BotID:    botID,
		Kind:     kind,
		Matcher:  task.Matcher,
		Schedule: task.Schedule,
		Alias:    task.Alias,
		Command:  task.Command,
		GroupID:  task.GroupID,
		UserID:   task.UserID,
	}, nil
}

func (r jobRow) storedJob() (storedJob, error) {
	kind, err := storedKindFromDatabase(r.Kind)
	if err != nil {
		return storedJob{}, err
	}
	return storedJob{
		ID:       r.TaskID,
		Kind:     kind,
		Matcher:  r.Matcher,
		Schedule: r.Schedule,
		Alias:    r.Alias,
		Command:  r.Command,
		GroupID:  r.GroupID,
		UserID:   r.UserID,
	}, nil
}

func jobKey(botID, taskID int64) string {
	return strconv.FormatInt(botID, 10) + ":" + strconv.FormatInt(taskID, 10)
}
