package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/FloatTech/floatbox/binary"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

type (
	// Type 任务类型,1=指令别名,2=定时任务,3=你问我答
	Type uint8
	// FullMatchType 指令别名类型, jobType=1使用的参数, 1=无状态消息, 2=主人消息
	FullMatchType uint8
	// QuestionType 问题类型, jobType=3使用的参数, 1=单独问, 2=所有人问
	QuestionType uint8
	// AnswerType 回答类型, jobType=3使用的参数, 1=文本消息, 2=注入消息
	AnswerType uint8
)

const (
	// FullMatchJob 指令别名
	FullMatchJob Type = iota + 1
	// CronJob 定时任务
	CronJob
	// RegexpJob 你问我答
	RegexpJob
)

const (
	// NoStateMsg 无状态消息
	NoStateMsg FullMatchType = iota + 1
	// SuperMsg 主人消息
	SuperMsg
)

const (
	// OneQuestion 单独问
	OneQuestion QuestionType = iota + 1
	// AllQuestion 所有人问
	AllQuestion
)

const (
	// TextMsg 文本消息
	TextMsg AnswerType = iota + 1
	// InjectMsg 注入消息
	InjectMsg
)

// Job 添加任务的入参
//
//	@Description 添加任务的入参
type Job struct {
	ID            string        `json:"id"`            // 任务id
	SelfID        int64         `json:"selfId"`        // 机器人id
	JobType       Type          `json:"jobType"`       // 任务类型,1-指令别名,2-定时任务,3-你问我答
	Matcher       string        `json:"matcher"`       // 指令别名、cron表达式或正则表达式
	Handler       string        `json:"handler"`       // 执行内容
	FullMatchType FullMatchType `json:"fullMatchType"` // 指令别名类型
	QuestionType  QuestionType  `json:"questionType"`  // 问题范围
	AnswerType    AnswerType    `json:"answerType"`    // 回答类型
	GroupID       int64         `json:"groupId"`       // 群聊id
	UserID        int64         `json:"userId"`        // 用户id
}

// List 任务列表
func List() (jobList []Job, err error) {
	jobList = make([]Job, 0, 16)
	zero.RangeBot(func(botID int64, _ *zero.Ctx) bool {
		stored, listErr := tasks.list(botID)
		if listErr != nil {
			err = listErr
			return false
		}
		for _, task := range stored {
			job, convertErr := task.publicJob(botID)
			if convertErr != nil {
				err = convertErr
				return false
			}
			jobList = append(jobList, job)
		}
		return true
	})
	return jobList, err
}

// Add 添加任务
func Add(j *Job) error {
	if j == nil {
		return errors.New("任务不能为空")
	}
	if zero.GetBot(j.SelfID) == nil {
		return fmt.Errorf("机器人 %d 未连接", j.SelfID)
	}
	task, err := newStoredJob(*j)
	if err != nil {
		return err
	}
	return tasks.add(j.SelfID, task)
}

func newStoredJob(j Job) (storedJob, error) {
	if j.Matcher == "" {
		return storedJob{}, errors.New("匹配条件不能为空")
	}
	task := storedJob{Matcher: j.Matcher}
	switch j.JobType {
	case FullMatchJob:
		switch j.FullMatchType {
		case NoStateMsg:
			task.Kind = storedFullMatch
			encoded, err := json.Marshal(j.Handler)
			if err != nil {
				return storedJob{}, err
			}
			task.Command = binary.BytesToString(encoded)
		case SuperMsg:
			task.Kind = storedSuperMatch
			if len(zero.BotConfig.SuperUsers) == 0 {
				return storedJob{}, errors.New("未配置主人账号")
			}
			event := newMessageEvent(j.SelfID, zero.BotConfig.SuperUsers[0], 0, j.Handler)
			encoded, err := json.Marshal(&event)
			if err != nil {
				return storedJob{}, err
			}
			task.Command = binary.BytesToString(encoded)
		default:
			return storedJob{}, fmt.Errorf("不存在的指令别名类型 %d", j.FullMatchType)
		}
	case CronJob:
		if j.GroupID < 0 || j.UserID < 0 {
			return storedJob{}, errors.New("群聊或用户 ID 不能为负数")
		}
		task.Kind = storedCron
		task.Schedule = j.Matcher
		task.GroupID = j.GroupID
		task.UserID = j.UserID
		event := newMessageEvent(j.SelfID, j.UserID, j.GroupID, j.Handler)
		encoded, err := json.Marshal(&event)
		if err != nil {
			return storedJob{}, err
		}
		task.Command = binary.BytesToString(encoded)
	case RegexpJob:
		if j.GroupID <= 0 {
			return storedJob{}, errors.New("正则问答必须指定群聊")
		}
		if j.UserID < 0 {
			return storedJob{}, errors.New("用户 ID 不能为负数")
		}
		task.GroupID = j.GroupID
		task.Command = message.EscapeCQCodeText(j.Handler)
		switch {
		case j.QuestionType == AllQuestion && j.AnswerType == TextMsg:
			task.Kind = storedRegexAllText
		case j.QuestionType == AllQuestion && j.AnswerType == InjectMsg:
			task.Kind = storedRegexAllInject
		case j.QuestionType == OneQuestion && j.AnswerType == TextMsg:
			task.Kind = storedRegexPrivateText
			task.UserID = j.UserID
		case j.QuestionType == OneQuestion && j.AnswerType == InjectMsg:
			task.Kind = storedRegexPrivateInject
			task.UserID = j.UserID
		default:
			return storedJob{}, fmt.Errorf("不存在的问答类型组合 %d/%d", j.QuestionType, j.AnswerType)
		}
		if task.UserID == 0 && j.QuestionType == OneQuestion {
			return storedJob{}, errors.New("单独问必须指定用户")
		}
	default:
		return storedJob{}, fmt.Errorf("不存在的任务类型 %d", j.JobType)
	}
	task.ID = idof(task.legacyCron(), task.Command)
	return task, nil
}

func newMessageEvent(selfID, userID, groupID int64, handler string) zero.Event {
	nativeMessage, _ := json.Marshal(handler)
	event := zero.Event{
		SelfID:        selfID,
		UserID:        userID,
		GroupID:       groupID,
		RawMessage:    handler,
		NativeMessage: nativeMessage,
		PostType:      "message",
		Sender:        &zero.User{ID: userID},
	}
	if groupID > 0 {
		event.MessageType = "group"
	} else {
		event.MessageType = "private"
		event.TargetID = selfID
	}
	return event
}

// DeleteReq 删除任务的入参
//
//	@Description 删除任务的入参
type DeleteReq struct {
	IDList []string `json:"idList" form:"idList"` // 任务id
	SelfID int64    `json:"selfId" form:"selfId"` // 机器人qq
}

// Delete 删除任务
func Delete(req *DeleteReq) error {
	if req == nil || len(req.IDList) == 0 {
		return nil
	}
	taskIDs := make([]int64, len(req.IDList))
	for i, id := range req.IDList {
		parsed, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return fmt.Errorf("无效任务 ID %q: %w", id, err)
		}
		taskIDs[i] = parsed
	}
	return tasks.delete(req.SelfID, taskIDs)
}
