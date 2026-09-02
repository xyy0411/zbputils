package job

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	zero "github.com/wdvxdr1123/ZeroBot"
)

type groupSelectionKey struct {
	BotID  int64
	UserID int64
}

func registerGroupSelection(ctx *zero.Ctx, raw string) error {
	ids, err := parseGroupIDs(raw)
	if err != nil {
		return err
	}
	key := groupSelectionKey{BotID: ctx.Event.SelfID, UserID: ctx.Event.UserID}
	return jobs.saveGroups(key.BotID, key.UserID, ids)
}

func consumeGroupSelection(ctx *zero.Ctx) ([]int64, bool) {
	key := groupSelectionKey{BotID: ctx.Event.SelfID, UserID: ctx.Event.UserID}
	groups, err := jobs.groups(key.BotID, key.UserID)
	if err != nil || len(groups) == 0 {
		return nil, false
	}
	return groups, true
}

func parseGroupIDs(raw string) ([]int64, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("至少需要一个群号")
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("无效群号 %q", part)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("群号 %d 重复", id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func selectedGroups(ctx *zero.Ctx) []int64 {
	if groups, ok := consumeGroupSelection(ctx); ok {
		return groups
	}
	return []int64{ctx.Event.GroupID}
}

func selectedGroupsOr(ctx *zero.Ctx, fallback int64) ([]int64, bool) {
	if groups, ok := consumeGroupSelection(ctx); ok {
		return groups, true
	}
	return []int64{fallback}, false
}

func setTaskGroup(task storedJob, groupID int64) storedJob {
	task.GroupID = groupID
	if task.Kind == storedCron {
		if event, err := decodeStoredEvent(task.Command); err == nil {
			event.GroupID = groupID
			if groupID > 0 {
				event.MessageType = "group"
			} else {
				event.MessageType = "private"
				event.TargetID = event.SelfID
			}
			if encoded, err := json.Marshal(&event); err == nil {
				task.Command = string(encoded)
			}
		}
	}
	task.ID = idof(task.legacyCron(), task.Command)
	if task.Kind == storedFullMatch || task.Kind == storedSuperMatch {
		task.ID = idof(task.legacyCron(), task.Command+":"+strconv.FormatInt(groupID, 10))
	}
	return task
}
