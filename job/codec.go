package job

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/FloatTech/floatbox/binary"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

type storedKind uint8

const (
	storedCron storedKind = iota
	storedFullMatch
	storedSuperMatch
	storedRegexAllText
	storedRegexPrivateText
	storedRegexAllInject
	storedRegexPrivateInject
)

type storedJob struct {
	ID       int64
	Kind     storedKind
	Matcher  string
	Schedule string
	Alias    string
	Command  string
	GroupID  int64
	UserID   int64
}

func (k storedKind) databaseName() (string, error) {
	switch k {
	case storedCron:
		return "cron", nil
	case storedFullMatch:
		return "full_match", nil
	case storedSuperMatch:
		return "super_match", nil
	case storedRegexAllText:
		return "regex_all_text", nil
	case storedRegexPrivateText:
		return "regex_private_text", nil
	case storedRegexAllInject:
		return "regex_all_inject", nil
	case storedRegexPrivateInject:
		return "regex_private_inject", nil
	default:
		return "", fmt.Errorf("unknown stored job kind %d", k)
	}
}

func storedKindFromDatabase(name string) (storedKind, error) {
	switch name {
	case "cron":
		return storedCron, nil
	case "full_match":
		return storedFullMatch, nil
	case "super_match":
		return storedSuperMatch, nil
	case "regex_all_text":
		return storedRegexAllText, nil
	case "regex_private_text":
		return storedRegexPrivateText, nil
	case "regex_all_inject":
		return storedRegexAllInject, nil
	case "regex_private_inject":
		return storedRegexPrivateInject, nil
	default:
		return storedCron, fmt.Errorf("unknown stored job kind %q", name)
	}
}

func decodeStoredCmd(c cmd) (storedJob, error) {
	j := storedJob{
		ID:      c.ID,
		Command: c.Cmd,
	}

	switch {
	case strings.HasPrefix(c.Cron, "fm:"):
		j.Kind = storedFullMatch
		j.Matcher = c.Cron[3:]
	case strings.HasPrefix(c.Cron, "sm:"):
		j.Kind = storedSuperMatch
		j.Matcher = c.Cron[3:]
	case strings.HasPrefix(c.Cron, "rm:"):
		j.Kind = storedRegexAllText
		if err := j.decodeGroupRegex(c.Cron); err != nil {
			return storedJob{}, err
		}
	case strings.HasPrefix(c.Cron, "im:"):
		j.Kind = storedRegexAllInject
		if err := j.decodeGroupRegex(c.Cron); err != nil {
			return storedJob{}, err
		}
	case strings.HasPrefix(c.Cron, "rp:"):
		j.Kind = storedRegexPrivateText
		if err := j.decodePrivateRegex(c.Cron); err != nil {
			return storedJob{}, err
		}
	case strings.HasPrefix(c.Cron, "ip:"):
		j.Kind = storedRegexPrivateInject
		if err := j.decodePrivateRegex(c.Cron); err != nil {
			return storedJob{}, err
		}
	default:
		j.Kind = storedCron
		j.Matcher = c.Cron
		j.Schedule, j.Alias, _ = strings.Cut(c.Cron, ":->")
	}
	return j, nil
}

func (j storedJob) legacyCron() string {
	switch j.Kind {
	case storedFullMatch:
		return "fm:" + j.Matcher
	case storedSuperMatch:
		return "sm:" + j.Matcher
	case storedRegexAllText:
		return "rm:" + strconv.FormatInt(j.GroupID, 36) + ":" + j.Matcher
	case storedRegexPrivateText:
		return "rp:" + strconv.FormatInt(j.UserID, 36) + ":" + strconv.FormatInt(j.GroupID, 36) + ":" + j.Matcher
	case storedRegexAllInject:
		return "im:" + strconv.FormatInt(j.GroupID, 36) + ":" + j.Matcher
	case storedRegexPrivateInject:
		return "ip:" + strconv.FormatInt(j.UserID, 36) + ":" + strconv.FormatInt(j.GroupID, 36) + ":" + j.Matcher
	case storedCron:
		if j.Alias != "" {
			return j.Schedule + ":->" + j.Alias
		}
		return j.Schedule
	default:
		return ""
	}
}

func (j *storedJob) decodeGroupRegex(encoded string) error {
	parts := strings.SplitN(encoded, ":", 3)
	if len(parts) != 3 {
		return fmt.Errorf("invalid group regex task %q", encoded)
	}
	groupID, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil {
		return fmt.Errorf("invalid group id in regex task %q: %w", encoded, err)
	}
	j.GroupID = groupID
	j.Matcher = parts[2]
	return nil
}

func (j *storedJob) decodePrivateRegex(encoded string) error {
	parts := strings.SplitN(encoded, ":", 4)
	if len(parts) != 4 {
		return fmt.Errorf("invalid private regex task %q", encoded)
	}
	userID, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil {
		return fmt.Errorf("invalid user id in regex task %q: %w", encoded, err)
	}
	groupID, err := strconv.ParseInt(parts[2], 36, 64)
	if err != nil {
		return fmt.Errorf("invalid group id in regex task %q: %w", encoded, err)
	}
	j.UserID = userID
	j.GroupID = groupID
	j.Matcher = parts[3]
	return nil
}

func (j storedJob) publicJob(selfID int64) (Job, error) {
	result := Job{
		ID:      strconv.FormatInt(j.ID, 10),
		SelfID:  selfID,
		Matcher: j.Matcher,
	}

	switch j.Kind {
	case storedFullMatch:
		result.JobType = FullMatchJob
		result.FullMatchType = NoStateMsg
		result.GroupID = j.GroupID
		result.Handler = decodeNativeHandler(j.Command)
	case storedSuperMatch:
		result.JobType = FullMatchJob
		result.FullMatchType = SuperMsg
		result.GroupID = j.GroupID
		event, err := decodeStoredEvent(j.Command)
		if err != nil {
			return Job{}, err
		}
		result.Handler = event.RawMessage
	case storedRegexAllText, storedRegexPrivateText, storedRegexAllInject, storedRegexPrivateInject:
		result.JobType = RegexpJob
		result.GroupID = j.GroupID
		result.UserID = j.UserID
		result.Handler = message.UnescapeCQCodeText(j.Command)
		if j.Kind == storedRegexAllText || j.Kind == storedRegexAllInject {
			result.QuestionType = AllQuestion
		} else {
			result.QuestionType = OneQuestion
		}
		if j.Kind == storedRegexAllInject || j.Kind == storedRegexPrivateInject {
			result.AnswerType = InjectMsg
		} else {
			result.AnswerType = TextMsg
		}
	case storedCron:
		result.JobType = CronJob
		event, err := decodeStoredEvent(j.Command)
		if err != nil {
			return Job{}, err
		}
		result.Handler = event.RawMessage
		result.GroupID = event.GroupID
		result.UserID = event.UserID
	default:
		return Job{}, fmt.Errorf("unknown stored job kind %d", j.Kind)
	}
	return result, nil
}

func decodeStoredEvent(command string) (zero.Event, error) {
	var event zero.Event
	if err := json.Unmarshal(binary.StringToBytes(command), &event); err != nil {
		return zero.Event{}, err
	}
	return event, nil
}

func decodeNativeHandler(command string) string {
	var handler string
	if err := json.Unmarshal(binary.StringToBytes(command), &handler); err == nil {
		return handler
	}
	return command
}
