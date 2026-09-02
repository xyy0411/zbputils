package job

import (
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/FloatTech/floatbox/binary"
	"github.com/FloatTech/floatbox/process"
	sql "github.com/FloatTech/sqlite"
	zero "github.com/wdvxdr1123/ZeroBot"
)

var ErrTaskConflict = errors.New("任务已存在")

type taskService struct {
	mu      sync.Mutex
	store   taskRepository
	runtime runtimeOperator
}

func (s *taskService) list(botID int64) ([]storedJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.list(botID)
}

func (s *taskService) restoreBot(botID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.store.list(botID)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := s.register(botID, task); err != nil {
			return fmt.Errorf("restore task %d for bot %d: %w", task.ID, botID, err)
		}
	}
	return nil
}

func (s *taskService) add(botID int64, task storedJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addBatchLocked(botID, []storedJob{task})
}

func (s *taskService) addBatch(botID int64, tasks []storedJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addBatchLocked(botID, tasks)
}

func (s *taskService) addBatchLocked(botID int64, tasks []storedJob) error {
	if len(tasks) == 0 {
		return nil
	}
	for i := range tasks {
		if tasks[i].ID == 0 {
			tasks[i].ID = idof(tasks[i].legacyCron(), tasks[i].Command)
		}
		if _, err := s.store.find(botID, tasks[i].ID); err == nil {
			return fmt.Errorf("%w: %d", ErrTaskConflict, tasks[i].ID)
		} else if !errors.Is(err, sql.ErrNullResult) {
			return err
		}
		for j := 0; j < i; j++ {
			if tasks[j].ID == tasks[i].ID {
				return fmt.Errorf("%w: %d", ErrTaskConflict, tasks[i].ID)
			}
		}
	}
	added := make([]storedJob, 0, len(tasks))
	for _, task := range tasks {
		if err := s.store.save(botID, task); err != nil {
			_ = s.store.delete(botID, taskIDs(added))
			return err
		}
		if err := s.register(botID, task); err != nil {
			for _, previous := range added {
				s.unregister(botID, previous)
			}
			_ = s.store.delete(botID, taskIDs(append(added, task)))
			return err
		}
		added = append(added, task)
	}
	return nil
}

func taskIDs(tasks []storedJob) []int64 {
	ids := make([]int64, len(tasks))
	for i := range tasks {
		ids[i] = tasks[i].ID
	}
	return ids
}

func (s *taskService) delete(botID int64, taskIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(botID, taskIDs)
}

func (s *taskService) deleteWhere(botID int64, match func(storedJob) (bool, error)) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.list(botID)
	if err != nil {
		return 0, err
	}
	taskIDs := make([]int64, 0, len(stored))
	for _, task := range stored {
		ok, err := match(task)
		if err != nil {
			return 0, err
		}
		if ok {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	if err := s.deleteLocked(botID, taskIDs); err != nil {
		return 0, err
	}
	return len(taskIDs), nil
}

func (s *taskService) deleteLocked(botID int64, taskIDs []int64) error {
	loaded := make([]storedJob, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, err := s.store.find(botID, taskID)
		if errors.Is(err, sql.ErrNullResult) {
			continue
		}
		if err != nil {
			return err
		}
		loaded = append(loaded, task)
	}
	if err := s.store.delete(botID, taskIDs); err != nil {
		return err
	}
	for _, task := range loaded {
		s.unregister(botID, task)
	}
	return nil
}

func (s *taskService) register(botID int64, task storedJob) error {
	key := runtimeKey{BotID: botID, TaskID: task.ID}
	switch task.Kind {
	case storedCron:
		bot := zero.GetBot(botID)
		if bot == nil {
			return fmt.Errorf("bot %d is not connected", botID)
		}
		entryID, err := process.CronTab.AddFunc(task.Schedule, inject(bot, binary.StringToBytes(task.Command)))
		if err != nil {
			return err
		}
		s.runtime.replaceCron(key, entryID)
	case storedFullMatch:
		matcher := en.OnFullMatch(task.Matcher, matchBotScope(botID, task.GroupID)).SetBlock(true)
		matcher.Handle(generalhandler(task.Command))
		s.runtime.replaceMatcher(key, (*zero.Matcher)(matcher))
	case storedSuperMatch:
		handler, err := superuserhandler(binary.StringToBytes(task.Command))
		if err != nil {
			return err
		}
		matcher := en.OnFullMatch(task.Matcher, matchBotScope(botID, task.GroupID)).SetBlock(true)
		matcher.Handle(handler)
		s.runtime.replaceMatcher(key, (*zero.Matcher)(matcher))
	case storedRegexAllText, storedRegexPrivateText, storedRegexAllInject, storedRegexPrivateInject:
		compiled, err := regexp.Compile(transformPattern(task.Matcher))
		if err != nil {
			return err
		}
		mu.Lock()
		group := global.ensure(botID, task.GroupID)
		entry := inst{
			TaskID:   task.ID,
			regex:    compiled,
			Pattern:  task.Matcher,
			Template: task.Command,
			IsInject: task.Kind == storedRegexAllInject || task.Kind == storedRegexPrivateInject,
		}
		if task.Kind == storedRegexAllText || task.Kind == storedRegexAllInject {
			group.All = append(group.All, entry)
		} else {
			group.Private[task.UserID] = append(group.Private[task.UserID], entry)
		}
		mu.Unlock()
	default:
		return fmt.Errorf("unknown stored job kind %d", task.Kind)
	}
	return nil
}

func (s *taskService) unregister(botID int64, task storedJob) {
	key := runtimeKey{BotID: botID, TaskID: task.ID}
	switch task.Kind {
	case storedCron:
		s.runtime.removeCron(key)
	case storedFullMatch, storedSuperMatch:
		s.runtime.removeMatcher(key)
	case storedRegexAllText, storedRegexPrivateText, storedRegexAllInject, storedRegexPrivateInject:
		mu.Lock()
		defer mu.Unlock()
		group := global.get(botID, task.GroupID)
		if group == nil {
			return
		}
		remove := func(entries []inst) []inst {
			for i := range entries {
				if entries[i].TaskID == task.ID {
					return append(entries[:i], entries[i+1:]...)
				}
			}
			return entries
		}
		if task.Kind == storedRegexAllText || task.Kind == storedRegexAllInject {
			group.All = remove(group.All)
		} else {
			group.Private[task.UserID] = remove(group.Private[task.UserID])
		}
	}
}

func matchBotScope(botID, groupID int64) zero.Rule {
	return func(ctx *zero.Ctx) bool {
		return ctx.Event.SelfID == botID && (groupID == 0 || ctx.Event.GroupID == groupID)
	}
}
