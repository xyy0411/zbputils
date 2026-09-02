// Package job 定时指令触发器
package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"strconv"
	"strings"
	"sync"
	"time"

	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"

	"github.com/FloatTech/floatbox/binary"
	"github.com/FloatTech/floatbox/process"
	"github.com/FloatTech/floatbox/web"

	"github.com/FloatTech/zbputils/control"
	"github.com/FloatTech/zbputils/vevent"
)

var (
	runtime = newRuntimeRegistry()
	mu      sync.RWMutex
	en      = control.Register("job", &ctrl.Options[*zero.Ctx]{
		DisableOnDefault:  false,
		Brief:             "定时指令触发器",
		Help:              "- 设置指令群 群号1,群号2\n- 查看指令群\n- 查看群指令\n- 删除我的全部群指令\n- 记录群指令在\"cron\"触发的指令\n- 记录群指令以\"完全匹配关键词\"触发的指令\n- 群指令[我|大家|有人][说|问][正则表达式]你[答|说|做|执行][模版]\n- 取消群指令在\"cron\"触发的指令\n- 取消群指令以\"完全匹配关键词\"触发的指令\n- 删除群指令[大家|有人|我][说|问|让你做|让你执行][正则表达式]\n- 记录以\"完全匹配关键词\"触发的指令\n- 记录在\"cron\"触发的指令\n- 取消以\"完全匹配关键词\"触发的指令\n- 取消在\"cron\"触发的指令\n- 查看所有触发指令\n- 查看在\"cron\"触发的指令\n- 查看以\"完全匹配关键词\"触发的指令\n- 注入指令结果：任意指令\n- 执行指令：任意指令\n- [我|大家|有人][说|问][正则表达式]你[答|说|做|执行][模版]\n- [查看|看看][我|大家|有人][说|问][正则表达式]\n- 删除[大家|有人|我][说|问|让你做|让你执行][正则表达式]",
		PrivateDataFolder: "job",
	})
)

func init() {
	err := db.Open(time.Hour)
	if err != nil {
		panic(err)
	}
	if err = jobs.initSchema(); err != nil {
		panic(err)
	}
	go func() {
		process.GlobalInitMutex.Lock()
		defer process.GlobalInitMutex.Unlock()
		process.SleepAbout1sTo2s()
		zero.RangeBot(func(id int64, _ *zero.Ctx) bool {
			if err := tasks.restoreBot(id); err != nil {
				logrus.Errorf("[job] restore tasks for bot %d failed: %v", id, err)
			}
			return true
		})
		logrus.Infoln("[job]本地环回初始化完成")
	}()
	registerCommandHandlers()
}

func registerCommandHandlers() {
	en.OnRegex(`^设置指令群\s*(.*)$`, zero.SuperUserPermission, zero.OnlyPrivate, func(ctx *zero.Ctx) bool {
		return strings.TrimSpace(ctx.State["regex_matched"].([]string)[1]) != ""
	}).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		raw := ctx.State["regex_matched"].([]string)[1]
		if err := registerGroupSelection(ctx, raw); err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("已设置，后续任务指令将永久应用到指定群"))
	})
	en.OnFullMatch("查看指令群", zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		groups, err := jobs.groups(ctx.Event.SelfID, ctx.Event.UserID)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		if len(groups) == 0 {
			ctx.SendChain(message.Text("尚未设置指令群"))
			return
		}
		parts := make([]string, len(groups))
		for i, groupID := range groups {
			parts[i] = strconv.FormatInt(groupID, 10)
		}
		ctx.SendChain(message.Text("已设置指令群: ", strings.Join(parts, ",")))
	})
	en.OnFullMatch("查看群指令", zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		stored, err := tasks.list(ctx.Event.SelfID)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lines := make([]string, 0)
		for _, task := range stored {
			if !task.Scoped || task.OwnerID != ctx.Event.UserID {
				continue
			}
			lines = append(lines, fmt.Sprintf("群 %d | %s | %s | 执行: %s", task.GroupID, task.legacyCron(), taskKindName(task.Kind), taskCommandText(task)))
		}
		if len(lines) == 0 {
			ctx.SendChain(message.Text("尚未设置群指令"))
			return
		}
		ctx.SendChain(message.Text(strings.Join(lines, "\n")))
	})
	en.OnFullMatch("删除我的全部群指令", zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		deleted, err := tasks.deleteWhere(ctx.Event.SelfID, func(task storedJob) (bool, error) {
			return task.Scoped && task.OwnerID == ctx.Event.UserID, nil
		})
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("已删除 ", deleted, " 条群指令"))
	})
	en.OnRegex(`^取消群指令在"(.*)"触发的指令$`, zero.SuperUserPermission, zero.OnlyPrivate, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := ctx.State["regex_matched"].([]string)[1]
		err := deleteScopedCron(ctx.Event.SelfID, ctx.Event.UserID, cron)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^取消群指令以"(.*)"触发的(代表我执行的)?指令$`, zero.SuperUserPermission, zero.OnlyPrivate, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		matched := ctx.State["regex_matched"].([]string)
		kind := storedFullMatch
		if matched[2] != "" {
			kind = storedSuperMatch
		}
		if err := deleteScopedMatch(ctx.Event.SelfID, ctx.Event.UserID, kind, matched[1]); err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^记录(群指令)?在"(.*)"触发的(别名.*的)?指令$`, zero.UserOrGrpAdmin, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := ctx.State["regex_matched"].([]string)[2]
		alias := ctx.State["regex_matched"].([]string)[3]
		command := ctx.State["job_raw_event"].(string)
		if alias != "" {
			cron += ":->" + alias[len("别名"):len(alias)-len("的")]
		}
		c := &cmd{
			ID:   idof(cron, command),
			Cron: cron,
			Cmd:  command,
		}
		err := addcmd(ctx, c)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^记录(群指令)?以"(.*)"触发的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := "fm:" + ctx.State["regex_matched"].([]string)[2]
		command := ctx.State["job_new_event"].(gjson.Result).Get("message").Raw
		logrus.Debugln("[job] get cmd:", command)
		c := &cmd{
			ID:   idof(cron, command),
			Cron: cron,
			Cmd:  command,
		}
		err := registercmd(ctx, c)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^记录(群指令)?以"(.*)"触发的代表我执行的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := "sm:" + ctx.State["regex_matched"].([]string)[2]
		command := ctx.State["job_raw_event"].(string)
		logrus.Debugln("[job] get cmd:", command)
		c := &cmd{
			ID:   idof(cron, command),
			Cron: cron,
			Cmd:  command,
		}
		err := registercmd(ctx, c)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^取消在"(.*)"触发的指令$`, zero.UserOrGrpAdmin, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := ctx.State["regex_matched"].([]string)[1]
		err := rmcmd(ctx.Event.SelfID, ctx.Event.UserID, cron, zero.AdminPermission(ctx))
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^取消以"(.*)"触发的(代表我执行的)?指令$`, zero.SuperUserPermission, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		issu := ctx.State["regex_matched"].([]string)[2] != ""
		cron := ""
		if issu {
			cron = "sm:"
		} else {
			cron = "fm:"
		}
		cron += ctx.State["regex_matched"].([]string)[1]
		err := delcmd(ctx.Event.SelfID, cron)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnFullMatch("查看所有触发指令", zero.SuperUserPermission).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		stored, err := tasks.list(ctx.Event.SelfID)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, len(stored)+2)
		if ctx.Event.GroupID != 0 {
			lst = append(lst, "在本群的触发指令]\n")
		} else {
			lst = append(lst, "全部触发指令]\n")
		}
		seen := make(map[string]struct{}, len(stored))
		for _, task := range stored {
			if ctx.Event.GroupID != 0 && task.Kind != storedFullMatch && task.Kind != storedSuperMatch && task.GroupID != ctx.Event.GroupID {
				continue
			}
			encoded := task.legacyCron()
			if _, ok := seen[encoded]; ok {
				continue
			}
			seen[encoded] = struct{}{}
			lst = append(lst, encoded+"\n")
		}
		lst = append(lst, "[END")
		ctx.SendChain(message.Text(lst))
	})
	en.OnRegex(`^查看在"(.*)"触发的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := ctx.State["regex_matched"].([]string)[1]
		stored, err := tasks.list(ctx.Event.SelfID)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, len(stored))
		for _, task := range stored {
			if task.Kind == storedCron && task.legacyCron() == cron {
				lst = append(lst, task.Command+"\n")
			}
		}
		ctx.SendChain(message.Text(lst))
	})
	en.OnRegex(`^查看以"(.*)"触发的(代表我执行的)?指令$`, zero.SuperUserPermission, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		issu := ctx.State["regex_matched"].([]string)[2] != ""
		kind := storedFullMatch
		if issu {
			kind = storedSuperMatch
		}
		matcher := ctx.State["regex_matched"].([]string)[1]
		stored, err := tasks.list(ctx.Event.SelfID)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, len(stored))
		for _, task := range stored {
			if task.Kind == kind && task.Matcher == matcher {
				lst = append(lst, task.Command+"\n")
			}
		}
		ctx.SendChain(message.Text(lst))
	})
	en.OnPrefix("执行指令：", zero.UserOrGrpAdmin, func(ctx *zero.Ctx) bool {
		return ctx.State["args"].(string) != ""
	}, parseArgs).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		ev := strings.ReplaceAll(ctx.Event.RawEvent.Raw, "执行指令：", "")
		logrus.Debugln("[job] inject:", ev)
		inject(ctx, binary.StringToBytes(ev))()
	})
	en.OnPrefix("注入指令结果：", zero.UserOrGrpAdmin, func(ctx *zero.Ctx) bool {
		return ctx.State["args"].(string) != ""
	}, parseArgs).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		hook := vevent.NewAPICallerReturnHook(ctx, func(_ zero.APIRequest, rsp zero.APIResponse, err error) {
			if err == nil {
				logrus.Debugln("[job] CallerHook returned")
				id := message.NewMessageIDFromInteger(rsp.Data.Get("message_id").Int())
				if id.ID() == 0 {
					ctx.SendChain(message.Text("ERROR:未获取到返回结果"))
					return
				}
				msg := ctx.GetMessage(id)
				ctx.Event.NativeMessage = json.RawMessage("\"" + msg.Elements.String() + "\"")
				ctx.Event.RawMessageID = json.RawMessage(msg.MessageID.String())
				ctx.Event.RawMessage = msg.Elements.String()
				process.SleepAbout1sTo2s() // 防止风控
				ctx.Event.Time = time.Now().Unix()
				ctx.DeleteMessage(id)
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
			}
		})
		hookedctx := zero.Ctx{Event: ctx.Event, State: ctx.State}
		vevent.HookCtxCaller(&hookedctx, hook)
		hookedctx.Echo(binary.StringToBytes(strings.ReplaceAll(ctx.Event.RawEvent.Raw, "注入指令结果：", "")))
	})
}

func isfirstregmatchnotnil(ctx *zero.Ctx) bool {
	matched := ctx.State["regex_matched"].([]string)
	if len(matched) > 2 {
		return matched[2] != ""
	}
	return len(matched) > 1 && matched[1] != ""
}

func inject(ctx *zero.Ctx, response []byte) func() {
	return func() { ctx.Echo(response) }
}

func idof(cron, cmd string) int64 {
	return int64(crc64.Checksum(binary.StringToBytes(cron+cmd), crc64.MakeTable(crc64.ISO)))
}

func addcmd(ctx *zero.Ctx, c *cmd) error {
	task, err := decodeStoredCmd(*c)
	if err != nil {
		return err
	}
	groups, selected := selectedGroupsOr(ctx, ctx.Event.GroupID)
	if strings.HasPrefix(ctx.MessageString(), "记录群指令") && !selected {
		return errors.New("请先设置指令群")
	}
	if !selected {
		groups = []int64{task.GroupID}
	}
	scoped := make([]storedJob, len(groups))
	for i, groupID := range groups {
		scoped[i] = setTaskGroup(task, groupID)
		scoped[i].Scoped = selected
		if selected {
			scoped[i].OwnerID = ctx.Event.UserID
		}
	}
	return tasks.addBatch(ctx.Event.SelfID, scoped)
}

func registercmd(ctx *zero.Ctx, c *cmd) error {
	task, err := decodeStoredCmd(*c)
	if err != nil {
		return err
	}
	bot := ctx.Event.SelfID
	groups, selected := selectedGroupsOr(ctx, 0)
	if strings.HasPrefix(ctx.MessageString(), "记录群指令") && !selected {
		return errors.New("请先设置指令群")
	}
	if !selected {
		groups = []int64{0}
	}
	scoped := make([]storedJob, len(groups))
	for i, groupID := range groups {
		scoped[i] = setTaskGroup(task, groupID)
		scoped[i].Scoped = selected
		if selected {
			scoped[i].OwnerID = ctx.Event.UserID
		}
	}
	return tasks.addBatch(bot, scoped)
}

func taskKindName(kind storedKind) string {
	switch kind {
	case storedCron:
		return "定时"
	case storedFullMatch:
		return "完全匹配"
	case storedSuperMatch:
		return "代表我执行"
	case storedRegexAllText:
		return "大家问"
	case storedRegexPrivateText:
		return "我问"
	case storedRegexAllInject:
		return "大家执行"
	case storedRegexPrivateInject:
		return "我执行"
	default:
		return "未知"
	}
}

func taskCommandText(task storedJob) string {
	if task.Kind == storedCron || task.Kind == storedSuperMatch {
		if event, err := decodeStoredEvent(task.Command); err == nil {
			return event.RawMessage
		}
	}
	if task.Kind >= storedRegexAllText {
		return message.UnescapeCQCodeText(task.Command)
	}
	return decodeNativeHandler(task.Command)
}

func deleteScopedCron(bot, owner int64, cron string) error {
	deleted, err := tasks.deleteWhere(bot, func(task storedJob) (bool, error) {
		return task.Scoped && task.OwnerID == owner && task.Kind == storedCron && task.Schedule == cron, nil
	})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return errors.New("没有找到对应的群指令")
	}
	return nil
}

func deleteScopedMatch(bot, owner int64, kind storedKind, matcher string) error {
	deleted, err := tasks.deleteWhere(bot, func(task storedJob) (bool, error) {
		return task.Scoped && task.OwnerID == owner && task.Kind == kind && task.Matcher == matcher, nil
	})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return errors.New("没有找到对应的群指令")
	}
	return nil
}

func generalhandler(command string) zero.Handler {
	cmdraw := make(json.RawMessage, len(command))
	copy(cmdraw, command)
	return func(ctx *zero.Ctx) {
		ctx.Event.NativeMessage = cmdraw
		ctx.Event.Time = time.Now().Unix()
		var encodeErr error
		vev, cl := binary.OpenWriterF(func(w *binary.Writer) {
			encodeErr = json.NewEncoder(w).Encode(ctx.Event)
		})
		if encodeErr != nil {
			cl()
			ctx.SendChain(message.Text("ERROR: ", encodeErr))
			return
		}
		logrus.Debugln("[job] inject:", binary.BytesToString(vev))
		defer func() {
			_ = recover()
			cl()
		}()
		ctx.Echo(vev)
	}
}

func superuserhandler(rsp []byte) (zero.Handler, error) {
	e := &zero.Event{Sender: new(zero.User)}
	err := json.Unmarshal(rsp, e)
	if err != nil {
		return nil, err
	}
	return func(ctx *zero.Ctx) {
		ctx.Event.UserID = e.UserID
		ctx.Event.RawMessage = e.RawMessage
		ctx.Event.Sender = e.Sender
		ctx.Event.NativeMessage = e.NativeMessage
		var encodeErr error
		vev, cl := binary.OpenWriterF(func(w *binary.Writer) {
			encodeErr = json.NewEncoder(w).Encode(ctx.Event)
		})
		if encodeErr != nil {
			cl()
			ctx.SendChain(message.Text("ERROR: ", encodeErr))
			return
		}
		logrus.Debugln("[job] inject:", binary.BytesToString(vev))
		defer func() {
			_ = recover()
			cl()
		}()
		ctx.Echo(vev)
	}, nil
}

func rmcmd(bot, caller int64, cron string, force bool) error {
	_, err := tasks.deleteWhere(bot, func(task storedJob) (bool, error) {
		if task.Kind != storedCron || task.Schedule != cron {
			return false, nil
		}
		event, err := decodeStoredEvent(task.Command)
		if err != nil {
			return false, err
		}
		if !force && event.UserID != caller {
			return false, nil
		}
		return true, nil
	})
	return err
}

func delcmd(bot int64, cron string) error {
	_, err := tasks.deleteWhere(bot, func(task storedJob) (bool, error) {
		return (task.Kind == storedFullMatch || task.Kind == storedSuperMatch) && task.legacyCron() == cron, nil
	})
	return err
}

func parseArgs(ctx *zero.Ctx) bool {
	cmds := ctx.State["args"].(string)
	if !strings.Contains(cmds, "?::") && !strings.Contains(cmds, "!::") {
		return true
	}
	args := make(map[int]string)
	for strings.Contains(ctx.Event.RawEvent.Raw, "?::") {
		start := strings.Index(ctx.Event.RawEvent.Raw, "?::")
		msgend := strings.Index(ctx.Event.RawEvent.Raw[start+3:], "::")
		if msgend < 0 {
			ctx.SendChain(message.Text("ERROR:找不到结束的::"))
			return false
		}
		msgend += start + 3
		numend := strings.Index(ctx.Event.RawEvent.Raw[msgend+2:], "!")
		if numend <= 0 {
			ctx.SendChain(message.Text("ERROR:找不到结束的!"))
			return false
		}
		numend += msgend + 2
		logrus.Debugln("[job]", start, msgend, numend)
		msg := ctx.Event.RawEvent.Raw[start+3 : msgend]
		arg, err := strconv.Atoi(ctx.Event.RawEvent.Raw[msgend+2 : numend])
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return false
		}
		arr, ok := args[arg]
		if !ok {
			var id message.ID
			if msg == "" {
				id = ctx.SendChain(message.At(ctx.Event.UserID), message.Text("请输入参数", arg))
			} else {
				id = ctx.SendChain(message.At(ctx.Event.UserID), message.Text("[", arg, "] ", msg))
			}
			select {
			case <-time.After(time.Second * 120):
				ctx.Send(message.ReplyWithMessage(id, message.Text("参数读取超时")))
				if len(msg) == 0 || msg[0] != '?' {
					return false
				}
			case c := <-zero.NewFutureEvent("message", 0, true, zero.CheckUser(ctx.Event.UserID)).Next():
				args[arg] = c.Event.Message.String()
				arr = args[arg]
				process.SleepAbout1sTo2s()
				ctx.SendChain(message.Reply(c.Event.MessageID), message.Text("已记录"))
				process.SleepAbout1sTo2s()
			}
		}
		ctx.Event.RawEvent.Raw = ctx.Event.RawEvent.Raw[:start] + arr + ctx.Event.RawEvent.Raw[numend+1:]
	}
	args = make(map[int]string)
	for strings.Contains(ctx.Event.RawEvent.Raw, "!::") {
		start := strings.Index(ctx.Event.RawEvent.Raw, "!::")
		msgend := strings.Index(ctx.Event.RawEvent.Raw[start+3:], "::")
		if msgend < 0 {
			ctx.SendChain(message.Text("ERROR:找不到结束的::"))
			return false
		}
		msgend += start + 3
		numend := strings.Index(ctx.Event.RawEvent.Raw[msgend+2:], "!")
		if numend <= 0 {
			ctx.SendChain(message.Text("ERROR:找不到结束的!"))
			return false
		}
		numend += msgend + 2
		logrus.Debugln("[job]", start, msgend, numend)
		u := ctx.Event.RawEvent.Raw[start+3 : msgend]
		if u == "" {
			return false
		}
		arg, err := strconv.Atoi(ctx.Event.RawEvent.Raw[msgend+2 : numend])
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return false
		}
		arr, ok := args[arg]
		if !ok {
			isnilable := u[0] == '?'
			if isnilable {
				u = u[1:]
				if u == "" {
					return false
				}
			}
			b, err := web.GetData(u)
			if err != nil {
				ctx.SendChain(message.Text("ERROR: ", err))
				if !isnilable {
					return false
				}
			}
			if len(b) > 0 {
				type fakejson struct {
					Arg string `json:"arg"`
				}
				f := fakejson{Arg: binary.BytesToString(b)}
				w := binary.SelectWriter()
				defer binary.PutWriter(w)
				_ = json.NewEncoder(w).Encode(&f)
				arr = w.String()[8 : w.Len()-3]
				args[arg] = arr
			}
		}
		w := binary.SelectWriter()
		w.WriteString(ctx.Event.RawEvent.Raw[:start])
		w.WriteString(arr)
		w.WriteString(ctx.Event.RawEvent.Raw[numend+1:])
		ctx.Event.RawEvent.Raw = string(w.Bytes())
		binary.PutWriter(w)
	}
	return true
}

func logevent(ctx *zero.Ctx) bool {
	matched := ctx.State["regex_matched"].([]string)
	cronIndex := 1
	if len(matched) > 2 {
		cronIndex = 2
	}
	ctx.SendChain(message.Text("您的下一条指令将被记录, 在", matched[cronIndex], "时触发"))
	select {
	case <-time.After(time.Second * 120):
		ctx.SendChain(message.Text("指令记录超时"))
		return false
	case c := <-zero.NewFutureEvent("message", 0, true, zero.CheckUser(ctx.Event.UserID)).Next():
		ctx.State["job_raw_event"] = c.Event.RawEvent.Raw
		ctx.State["job_new_event"] = c.Event.RawEvent
		return true
	}
}
