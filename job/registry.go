package job

import (
	"sync"

	"github.com/FloatTech/floatbox/process"
	"github.com/fumiama/cron"
	zero "github.com/wdvxdr1123/ZeroBot"
)

type runtimeKey struct {
	BotID  int64
	TaskID int64
}

type runtimeRegistry struct {
	mu       sync.Mutex
	cron     map[runtimeKey]cron.EntryID
	matchers map[runtimeKey]*zero.Matcher
}

type runtimeOperator interface {
	replaceCron(key runtimeKey, entryID cron.EntryID)
	removeCron(key runtimeKey) bool
	replaceMatcher(key runtimeKey, matcher *zero.Matcher)
	removeMatcher(key runtimeKey) bool
}

func newRuntimeRegistry() *runtimeRegistry {
	return &runtimeRegistry{
		cron:     make(map[runtimeKey]cron.EntryID),
		matchers: make(map[runtimeKey]*zero.Matcher),
	}
}

func (r *runtimeRegistry) replaceCron(key runtimeKey, entryID cron.EntryID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.cron[key]; ok {
		process.CronTab.Remove(old)
	}
	r.cron[key] = entryID
}

func (r *runtimeRegistry) removeCron(key runtimeKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entryID, ok := r.cron[key]
	if !ok {
		return false
	}
	process.CronTab.Remove(entryID)
	delete(r.cron, key)
	return true
}

func (r *runtimeRegistry) replaceMatcher(key runtimeKey, matcher *zero.Matcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.matchers[key]; ok {
		old.Delete()
	}
	r.matchers[key] = matcher
}

func (r *runtimeRegistry) removeMatcher(key runtimeKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	matcher, ok := r.matchers[key]
	if !ok {
		return false
	}
	matcher.Delete()
	delete(r.matchers, key)
	return true
}
