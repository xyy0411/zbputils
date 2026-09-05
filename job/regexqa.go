package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	"github.com/FloatTech/floatbox/binary"
	"github.com/FloatTech/floatbox/process"
	"github.com/FloatTech/zbputils/ctxext"
)

var global = context{
	group: make(map[questionGroupKey]*regexGroup),
}

type context struct {
	group map[questionGroupKey]*regexGroup
}

type questionGroupKey struct {
	BotID   int64
	GroupID int64
}

type regexGroup struct {
	All     []inst
	Private map[int64][]inst
}

type inst struct {
	TaskID   int64
	regex    *regexp.Regexp
	Pattern  string
	Template string
	IsInject bool
}

func (c *context) get(botID, groupID int64) *regexGroup {
	return c.group[questionGroupKey{BotID: botID, GroupID: groupID}]
}

func (c *context) ensure(botID, groupID int64) *regexGroup {
	key := questionGroupKey{BotID: botID, GroupID: groupID}
	group := c.group[key]
	if group == nil {
		group = &regexGroup{Private: make(map[int64][]inst)}
		c.group[key] = group
	} else if group.Private == nil {
		group.Private = make(map[int64][]inst)
	}
	return group
}

var transformRegex = regexp.MustCompile(`<<.+?>>`)

// ErrRegexNotFound 没有找到对应的问答词条
var ErrRegexNotFound = errors.New("没有找到对应的问答词条")

func transformPattern(pattern string) string {
	pattern = transformRegex.ReplaceAllStringFunc(pattern, func(s string) string {
		s = strings.Trim(s, "<>")
		return `(?P<` + s + `>.+?)`
	})
	return "^" + pattern + "$"
}

func newRegexTask(groupID, userID int64, pattern, template string, inject bool) storedJob {
	kind := storedRegexAllText
	if userID > 0 {
		kind = storedRegexPrivateText
	}
	if inject && userID == 0 {
		kind = storedRegexAllInject
	} else if inject {
		kind = storedRegexPrivateInject
	}
	task := storedJob{
		Kind:    kind,
		Matcher: pattern,
		Command: template,
		GroupID: groupID,
		UserID:  userID,
	}
	task.ID = idof(task.legacyCron(), task.Command)
	return task
}

func deleteRegexTasks(botID, groupID, userID int64, patterns []string, inject bool) error {
	matchesPattern := func(pattern string) bool {
		for _, candidate := range patterns {
			if pattern == candidate {
				return true
			}
		}
		return false
	}
	deleted, err := tasks.deleteWhere(botID, func(task storedJob) (bool, error) {
		isRegex := task.Kind == storedRegexAllText || task.Kind == storedRegexPrivateText || task.Kind == storedRegexAllInject || task.Kind == storedRegexPrivateInject
		isInject := task.Kind == storedRegexAllInject || task.Kind == storedRegexPrivateInject
		return isRegex && isInject == inject && task.GroupID == groupID && task.UserID == userID && matchesPattern(task.Matcher), nil
	})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrRegexNotFound
	}
	return nil
}

func init() {
	registerRegexQuestionHandlers()
}

func registerRegexQuestionHandlers() {
	en.OnRegex(`^(群指令)?(我|大家|有人)(说|问)(.*)你(答|说|做|执行)`,
		func(ctx *zero.Ctx) bool {
			matched := ctx.State["regex_matched"].([]string)
			if matched[1] == "群指令" {
				return true
			}
			return zero.OnlyToMe(ctx) && zero.OnlyGroup(ctx)
		}).Limit(ctxext.LimitByGroup).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		all := true
		if matched[2] == "我" {
			all = false
		}
		if all && !zero.AdminPermission(ctx) {
			ctx.SendChain(message.Text("非管理员/主人无法设置全局问答"))
			return
		}
		isInject := false
		if matched[5] == "做" || matched[5] == "执行" {
			if !zero.AdminPermission(ctx) {
				ctx.SendChain(message.Text("非管理员/主人无法设置注入"))
				return
			}
			isInject = true
		}
		gid := ctx.Event.GroupID
		uid := ctx.Event.UserID
		pattern := message.UnescapeCQCodeText(matched[4])
		template := strings.TrimPrefix(ctx.MessageString(), matched[0])
		if all {
			uid = 0
		}
		groups, selected := selectedGroupsOr(ctx, gid)
		if matched[1] != "" && !selected {
			ctx.SendChain(message.Text("ERROR:请先设置指令群"))
			return
		}
		if !selected {
			groups = []int64{gid}
		}
		batch := make([]storedJob, len(groups))
		for i, groupID := range groups {
			batch[i] = newRegexTask(groupID, uid, pattern, template, isInject)
			batch[i].Scoped = selected
			if selected {
				batch[i].OwnerID = ctx.Event.UserID
			}
		}
		err := tasks.addBatch(ctx.Event.SelfID, batch)
		if err != nil {
			ctx.SendChain(message.Text("ERROR:无法保存正则表达式:", err))
			return
		}
		ctx.SendChain(message.Text("成功"))
	})

	en.OnRegex(`^(查看|看看)(我|大家|有人)(说|问)`, zero.OnlyGroup, zero.OnlyToMe).Limit(ctxext.LimitByGroup).Handle(func(ctx *zero.Ctx) {
		mu.RLock()
		defer mu.RUnlock()

		gid := ctx.Event.GroupID
		uid := ctx.Event.UserID
		matched := ctx.State["regex_matched"].([]string)
		all := true
		if matched[2] == "我" {
			all = false
		}
		arg := strings.TrimPrefix(ctx.MessageString(), matched[0])
		rg := global.get(ctx.Event.SelfID, gid)
		if rg == nil {
			return
		}

		w := binary.SelectWriter()
		defer binary.PutWriter(w)
		if all {
			w.WriteString("该群设置的“有人问”有：\n")
		} else {
			_, _ = fmt.Fprintf(w, "你在该群设置的含有 %s 的问题有：\n", arg)
		}
		show := func(insts []inst) {
			for i := range insts {
				if strings.Contains(insts[i].Pattern, arg) {
					w.WriteString(strings.Trim(insts[i].Pattern, "^$"))
					if insts[i].IsInject {
						w.WriteString("(做)")
					}
					_ = w.WriteByte('\n')
				}
			}
		}

		if all {
			show(rg.All)
		} else {
			show(rg.Private[uid])
		}
		ctx.SendChain(message.Text(w.String()))
	})

	en.OnRegex(`^删除群指令(大家|有人|我)(说|问|让你做|让你执行)`, zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		pattern := strings.TrimPrefix(ctx.MessageString(), matched[0])
		escaped := message.UnescapeCQCodeText(pattern)
		patterns := []string{pattern}
		if escaped != pattern {
			patterns = append(patterns, escaped)
		}
		uid := int64(0)
		if matched[1] == "我" {
			uid = ctx.Event.UserID
		}
		injecting := matched[2] == "让你做" || matched[2] == "让你执行"
		deleted, err := tasks.deleteWhere(ctx.Event.SelfID, func(task storedJob) (bool, error) {
			isRegex := task.Kind == storedRegexAllText || task.Kind == storedRegexPrivateText || task.Kind == storedRegexAllInject || task.Kind == storedRegexPrivateInject
			isInject := task.Kind == storedRegexAllInject || task.Kind == storedRegexPrivateInject
			if !task.Scoped || task.OwnerID != ctx.Event.UserID || !isRegex || isInject != injecting || task.UserID != uid {
				return false, nil
			}
			for _, candidate := range patterns {
				if task.Matcher == candidate {
					return true, nil
				}
			}
			return false, nil
		})
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		if deleted == 0 {
			ctx.SendChain(message.Text("ERROR: 没有找到对应的群指令"))
			return
		}
		ctx.SendChain(message.Text("删除成功"))
	})

	en.OnRegex(`^删除(大家|有人|我)(说|问|让你做|让你执行)`, zero.OnlyGroup, zero.OnlyToMe).Limit(ctxext.LimitByGroup).Handle(func(ctx *zero.Ctx) {
		gid := ctx.Event.GroupID
		uid := ctx.Event.UserID
		matched := ctx.State["regex_matched"].([]string)
		pattern := strings.TrimPrefix(ctx.MessageString(), matched[0])
		escapedpattern := message.UnescapeCQCodeText(pattern)
		if pattern == escapedpattern {
			escapedpattern = ""
		}
		all := true
		if matched[1] == "我" {
			all = false
		}
		if all && !zero.AdminPermission(ctx) {
			ctx.SendChain(message.Text("非管理员/主人无法删除全局问答"))
			return
		}
		isInject := false
		if matched[2] == "让你做" || matched[2] == "让你执行" {
			if !zero.AdminPermission(ctx) {
				ctx.SendChain(message.Text("非管理员/主人无法删除注入"))
				return
			}
			isInject = true
		}
		if all {
			uid = 0
		}
		patterns := []string{pattern}
		if escapedpattern != "" {
			patterns = append(patterns, escapedpattern)
		}
		err := deleteRegexTasks(ctx.Event.SelfID, gid, uid, patterns, isInject)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("删除成功"))
	})

	en.On(`message/group`, func(ctx *zero.Ctx) bool {
		mu.RLock()
		defer mu.RUnlock()

		gid := ctx.Event.GroupID
		uid := ctx.Event.UserID
		rg := global.get(ctx.Event.SelfID, gid)
		if rg == nil {
			return false
		}
		if runInsts(ctx, rg.All) {
			return true
		}
		return runInsts(ctx, rg.Private[uid])
	}).Limit(ctxext.LimitByGroup).Handle(func(ctx *zero.Ctx) {
		template := ctx.State["regqa_template"].(string)
		if ctx.State["regqa_isinject"].(bool) {
			ctx.Event.NativeMessage = json.RawMessage("\"" + template + "\"")
			ctx.Event.RawMessage = template
			process.SleepAbout1sTo2s() // 防止风控
			ctx.Event.Time = time.Now().Unix()
			var err error
			vev, cl := binary.OpenWriterF(func(w *binary.Writer) {
				err = json.NewEncoder(w).Encode(ctx.Event)
			})
			if err != nil {
				cl()
				ctx.SendChain(message.Text("ERROR: ", err))
				return
			}
			logrus.Debugln("[job] inject:", binary.BytesToString(vev))
			defer func() {
				_ = recover()
				cl()
			}()
			ctx.Echo(vev)
		} else {
			ctx.Send(template)
		}
	})
}

func runInsts(ctx *zero.Ctx, insts []inst) bool {
	msg := ctx.MessageString()
	for _, inst := range insts {
		if matched := inst.regex.FindStringSubmatch(msg); matched != nil {
			template := inst.Template
			sub := inst.regex.SubexpNames()
			for i := 1; i < len(matched); i++ {
				if sub[i] != "" {
					template = strings.ReplaceAll(template, "<<"+sub[i]+">>", matched[i])
				}
				template = strings.ReplaceAll(template, "$"+strconv.Itoa(i), matched[i])
			}
			ctx.State["regqa_template"] = template
			ctx.State["regqa_isinject"] = inst.IsInject
			return true
		}
	}
	return false
}
