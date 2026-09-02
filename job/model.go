package job

import sql "github.com/FloatTech/sqlite"

type cmd struct {
	ID   int64  `db:"id"`
	Cron string `db:"cron"`
	Cmd  string `db:"cmd"`
}

type customGroup struct {
	ID      int64 `db:"id"`
	BotID   int64 `db:"bot_id"`
	GroupID int64 `db:"group_id"`
}

type customTrigger struct {
	ID      int64  `db:"id"`
	BotID   int64  `db:"bot_id"`
	Trigger string `db:"trigger"`
	Command string `db:"command"`
}

var db = sql.New(en.DataFolder() + "job.db")

var jobs = repository{db: &db}

var tasks = taskService{store: jobs, runtime: runtime}
