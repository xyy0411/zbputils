// Package job 定时指令触发器
package job

import (
	"encoding/json"
	"errors"
	"hash/crc64"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sql "github.com/FloatTech/sqlite"
	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/fumiama/cron"
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

const help = `- 记录以"完全匹配关键词"触发的指令
- 取消以"完全匹配关键词"触发的指令
- 记录在"cron"触发的(别名xxx的)指令
- 取消在"cron"触发的指令
- 查看所有触发指令
- 查看在"cron"触发的指令
- 查看以"完全匹配关键词"触发的指令
- 注入指令结果：任意指令
- 执行指令：任意指令
- [我|大家|有人][说|问][正则表达式]你[答|说|做|执行][模版]
- [查看|看看][我|大家|有人][说|问][正则表达式]
- 删除[大家|有人|我][说|问|让你做|让你执行][正则表达式]
- 添加群 [群号]（仅主人私聊）
- 删除群 [群号]（仅主人私聊）
- 添加群触发器 "触发词" 执行指令（仅主人私聊）
- 删除群触发器 "触发词"（仅主人私聊）
- 查看群列表（仅主人私聊）
- 查看群触发器（仅主人私聊）`

var (
	entries        = map[int64]cron.EntryID{} // id entryid
	matchers       = map[int64]*zero.Matcher{}
	customGroups   = map[int64]map[int64]bool{} // bot -> group id
	customTriggers = map[int64]map[int64]customTrigger{}
	mu             sync.RWMutex
	en             = control.Register("job", &ctrl.Options[*zero.Ctx]{
		DisableOnDefault:  false,
		Brief:             "定时指令触发器",
		Help:              help,
		PrivateDataFolder: "job",
	})
)

func init() {
	err := db.Open(time.Hour)
	if err != nil {
		panic(err)
	}
	go func() {
		process.GlobalInitMutex.Lock()
		process.SleepAbout1sTo2s()
		zero.RangeBot(func(id int64, _ *zero.Ctx) bool {
			ids := strconv.FormatInt(id, 36)
			c := &cmd{}
			err := db.Create(ids, c)
			logrus.Debugln("[job]创建表", ids)
			if err != nil {
				panic(err)
			}
			err = db.FindFor(ids, c, "", func() error {
				mu.Lock()
				defer mu.Unlock()
				if strings.HasPrefix(c.Cron, "fm:") {
					m := en.OnFullMatch(c.Cron[3:] /* skip fm: */).SetBlock(true)
					m.Handle(generalhandler(c.Cmd))
					matchers[c.ID] = (*zero.Matcher)(m)
					return nil
				}
				if strings.HasPrefix(c.Cron, "sm:") {
					m := en.OnFullMatch(c.Cron[3:] /* skip sm: */).SetBlock(true)
					h, err := superuserhandler(binary.StringToBytes(c.Cmd))
					if err != nil {
						return nil
					}
					m.Handle(h)
					matchers[c.ID] = (*zero.Matcher)(m)
					return nil
				}
				if strings.HasPrefix(c.Cron, "rm:") || strings.HasPrefix(c.Cron, "im:") {
					patterns := strings.SplitN(c.Cron, ":", 3)
					if len(patterns) != 3 {
						return errors.New("error regex match global pattern")
					}
					grp, err := strconv.ParseInt(patterns[1], 36, 64)
					if err != nil {
						return err
					}
					if global.group[grp] == nil {
						global.group[grp] = new(regexGroup)
					}
					tmpl := make([]byte, len(c.Cmd))
					copy(tmpl, c.Cmd)
					compiled, err := regexp.Compile(transformPattern(patterns[2]))
					if err != nil {
						logrus.WithError(err).Errorf("[job]跳过无效的全局正则任务 %d", c.ID)
						return nil
					}
					global.group[grp].All = append(global.group[grp].All, inst{
						regex:    compiled,
						Pattern:  patterns[2],
						Template: binary.BytesToString(tmpl),
						IsInject: patterns[0][0] == 'i',
					})
					return nil
				}
				if strings.HasPrefix(c.Cron, "rp:") || strings.HasPrefix(c.Cron, "ip:") {
					patterns := strings.SplitN(c.Cron, ":", 4)
					if len(patterns) != 4 {
						return errors.New("error regex match private pattern")
					}
					uid, err := strconv.ParseInt(patterns[1], 36, 64)
					if err != nil {
						return err
					}
					gid, err := strconv.ParseInt(patterns[2], 36, 64)
					if err != nil {
						return err
					}
					if global.group[gid] == nil {
						global.group[gid] = new(regexGroup)
					}
					tmpl := make([]byte, len(c.Cmd))
					copy(tmpl, c.Cmd)
					if global.group[gid].Private == nil {
						global.group[gid].Private = make(map[int64][]inst)
					}
					compiled, err := regexp.Compile(transformPattern(patterns[3]))
					if err != nil {
						logrus.WithError(err).Errorf("[job]跳过无效的私有正则任务 %d", c.ID)
						return nil
					}
					global.group[gid].Private[uid] = append(global.group[gid].Private[uid], inst{
						regex:    compiled,
						Pattern:  patterns[3],
						Template: binary.BytesToString(tmpl),
						IsInject: patterns[0][0] == 'i',
					})
					return nil
				}
				cr, _, _ := strings.Cut(c.Cron, ":->")
				eid, err := process.CronTab.AddFunc(cr, inject(zero.GetBot(id), []byte(c.Cmd)))
				if err != nil {
					return err
				}
				entries[c.ID] = eid
				return nil
			})
			if err != nil && !errors.Is(err, sql.ErrNullResult) {
				panic(err)
			}
			return true
		})
		_ = db.Create("custom_groups", &customGroup{})
		_ = db.Create("custom_triggers", &customTrigger{})
		g := &customGroup{}
		t := &customTrigger{}
		mu.Lock()
		_ = db.FindFor("custom_groups", g, "", func() error {
			if customGroups[g.BotID] == nil {
				customGroups[g.BotID] = make(map[int64]bool)
			}
			customGroups[g.BotID][g.GroupID] = true
			return nil
		})
		_ = db.FindFor("custom_triggers", t, "", func() error {
			if customTriggers[t.BotID] == nil {
				customTriggers[t.BotID] = make(map[int64]customTrigger)
			}
			customTriggers[t.BotID][t.ID] = *t
			return nil
		})
		mu.Unlock()
		logrus.Infoln("[job]本地环回初始化完成")
		process.GlobalInitMutex.Unlock()
	}()
	en.OnRegex(`^添加群\s+(\d+)$`, zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(addCustomGroup)
	en.OnRegex(`^删除群\s+(\d+)$`, zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(deleteCustomGroup)
	en.OnRegex(`^添加群触发器\s+"([^"]+)"\s+(.+)$`, zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(addCustomTrigger)
	en.OnRegex(`^删除群触发器\s+"([^"]+)"$`, zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(deleteCustomTrigger)
	en.OnFullMatch("查看群列表", zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(listCustomGroups)
	en.OnFullMatch("查看群触发器", zero.SuperUserPermission, zero.OnlyPrivate).SetBlock(true).Handle(listCustomTriggers)
	en.On(`message/group`, func(ctx *zero.Ctx) bool {
		mu.RLock()
		defer mu.RUnlock()
		if !customGroups[ctx.Event.SelfID][ctx.Event.GroupID] {
			return false
		}
		for _, j := range customTriggers[ctx.Event.SelfID] {
			if j.Trigger == ctx.MessageString() {
				ctx.State["job_custom_command"] = j.Command
				return true
			}
		}
		return false
	}).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		command := ctx.State["job_custom_command"].(string)
		ctx.SendChain(message.ParseMessageFromString(command)...)
	})
	en.OnRegex(`^记录在"(.*)"触发的(别名.*的)?指令$`, zero.UserOrGrpAdmin, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := ctx.State["regex_matched"].([]string)[1]
		alias := ctx.State["regex_matched"].([]string)[2]
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
	en.OnRegex(`^记录以"(.*)"触发的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := "fm:" + ctx.State["regex_matched"].([]string)[1]
		command := ctx.State["job_new_event"].(gjson.Result).Get("message").Raw
		logrus.Debugln("[job] get cmd:", command)
		c := &cmd{
			ID:   idof(cron, command),
			Cron: cron,
			Cmd:  command,
		}
		err := registercmd(ctx.Event.SelfID, c)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text("成功!"))
	})
	en.OnRegex(`^记录以"(.*)"触发的代表我执行的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil, logevent).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		cron := "sm:" + ctx.State["regex_matched"].([]string)[1]
		command := ctx.State["job_raw_event"].(string)
		logrus.Debugln("[job] get cmd:", command)
		c := &cmd{
			ID:   idof(cron, command),
			Cron: cron,
			Cmd:  command,
		}
		err := registercmd(ctx.Event.SelfID, c)
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
		c := &cmd{}
		ids := strconv.FormatInt(ctx.Event.SelfID, 36)
		mu.Lock()
		defer mu.Unlock()
		n, err := db.Count(ids)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, n+2)
		q := ""
		var args []any
		if ctx.Event.GroupID != 0 {
			grp := strconv.FormatInt(ctx.Event.GroupID, 36)
			q = "WHERE cron LIKE 'fm:%' OR cron LIKE 'sm:%' OR cron LIKE ? OR cron LIKE ? OR cron LIKE ? OR cron LIKE ? "
			args = []any{"rm:" + grp + ":%", "im:" + grp + ":%", "rp:" + grp + ":%", "ip:" + grp + ":%"}
			lst = append(lst, "在本群的触发指令]\n")
		} else {
			lst = append(lst, "全部触发指令]\n")
		}
		q += "GROUP BY cron"
		err = db.FindFor(ids, c, q, func() error {
			lst = append(lst, c.Cron+"\n")
			return nil
		}, args...)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst = append(lst, "[END")
		ctx.SendChain(message.Text(lst))
	})
	en.OnRegex(`^查看在"(.*)"触发的指令$`, zero.SuperUserPermission, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		c := &cmd{}
		ids := strconv.FormatInt(ctx.Event.SelfID, 36)
		cron := ctx.State["regex_matched"].([]string)[1]
		mu.Lock()
		defer mu.Unlock()
		n, err := db.Count(ids)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, n)
		err = db.FindFor(ids, c, "WHERE cron = ?", func() error {
			lst = append(lst, c.Cmd+"\n")
			return nil
		}, cron)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		ctx.SendChain(message.Text(lst))
	})
	en.OnRegex(`^查看以"(.*)"触发的(代表我执行的)?指令$`, zero.SuperUserPermission, isfirstregmatchnotnil).SetBlock(true).Handle(func(ctx *zero.Ctx) {
		c := &cmd{}
		ids := strconv.FormatInt(ctx.Event.SelfID, 36)
		issu := ctx.State["regex_matched"].([]string)[2] != ""
		cron := ""
		if issu {
			cron = "sm:"
		} else {
			cron = "fm:"
		}
		cron += ctx.State["regex_matched"].([]string)[1]
		mu.Lock()
		defer mu.Unlock()
		n, err := db.Count(ids)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
		}
		lst := make([]string, 0, n)
		err = db.FindFor(ids, c, "WHERE cron = ?", func() error {
			lst = append(lst, c.Cmd+"\n")
			return nil
		}, cron)
		if err != nil {
			ctx.SendChain(message.Text("ERROR: ", err))
			return
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
		hookedctx := *ctx //nolint: govet
		vevent.HookCtxCaller(&hookedctx, hook)
		hookedctx.Echo(binary.StringToBytes(strings.ReplaceAll(ctx.Event.RawEvent.Raw, "注入指令结果：", "")))
	})
}

func isfirstregmatchnotnil(ctx *zero.Ctx) bool {
	return ctx.State["regex_matched"].([]string)[1] != ""
}

func inject(ctx *zero.Ctx, response []byte) func() {
	return func() { ctx.Echo(response) }
}

func idof(cron, cmd string) int64 {
	return int64(crc64.Checksum(binary.StringToBytes(cron+cmd), crc64.MakeTable(crc64.ISO)))
}

func addcmd(ctx *zero.Ctx, c *cmd) error {
	mu.Lock()
	defer mu.Unlock()
	cr, _, _ := strings.Cut(c.Cron, ":->")
	eid, err := process.CronTab.AddFunc(cr, inject(ctx, []byte(c.Cmd)))
	if err != nil {
		return err
	}
	entries[c.ID] = eid
	return db.Insert(strconv.FormatInt(ctx.Event.SelfID, 36), c)
}

func registercmd(bot int64, c *cmd) error {
	mu.Lock()
	defer mu.Unlock()
	m := en.OnFullMatch(c.Cron[3:] /* skip fm: or sm: */).SetBlock(true)
	if strings.HasPrefix(c.Cron, "sm:") {
		h, err := superuserhandler(binary.StringToBytes(c.Cmd))
		if err != nil {
			return err
		}
		m.Handle(h)
	} else {
		m.Handle(generalhandler(c.Cmd))
	}
	matchers[c.ID] = (*zero.Matcher)(m)
	return db.Insert(strconv.FormatInt(bot, 36), c)
}

func generalhandler(command string) zero.Handler {
	cmdraw := make(json.RawMessage, len(command))
	copy(cmdraw, command)
	return func(ctx *zero.Ctx) {
		ctx.Event.NativeMessage = cmdraw
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
	c := &cmd{}
	mu.Lock()
	defer mu.Unlock()
	bots := strconv.FormatInt(bot, 36)
	e := new(zero.Event)
	var delids []int64
	err := db.FindFor(bots, c, "WHERE cron = ? OR cron LIKE ?", func() error {
		err := json.Unmarshal(binary.StringToBytes(c.Cmd), e)
		if err != nil {
			return err
		}
		if !force && e.UserID != caller {
			return nil
		}
		eid, ok := entries[c.ID]
		if ok {
			process.CronTab.Remove(eid)
			delete(entries, c.ID)
		}
		delids = append(delids, c.ID)
		return nil
	}, cron, cron+":->%")
	if err != nil {
		return err
	}
	if len(delids) > 0 {
		return deletecmds(bots, delids)
	}
	return nil
}

func delcmd(bot int64, cron string) error {
	c := &cmd{}
	mu.Lock()
	defer mu.Unlock()
	bots := strconv.FormatInt(bot, 36)
	var delids []int64
	err := db.FindFor(bots, c, "WHERE cron = ?", func() error {
		m, ok := matchers[c.ID]
		if ok {
			m.Delete()
			delete(matchers, c.ID)
		}
		delids = append(delids, c.ID)
		return nil
	}, cron)
	if err != nil {
		return err
	}
	if len(delids) > 0 {
		return deletecmds(bots, delids)
	}
	return nil
}

func deletecmds(table string, ids []int64) error {
	q, s := sql.QuerySet("WHERE id", "IN", ids)
	err := db.Del(table, q, s...)
	if err == nil || !strings.Contains(err.Error(), "readonly database") {
		return err
	}
	// SQLITE_READONLY_DBMOVED means the database file was replaced or moved
	// after SQLite opened it. Reopen once and retry against the current file.
	if err = db.Close(); err != nil {
		return err
	}
	if err = db.Open(time.Hour); err != nil {
		return err
	}
	return db.Del(table, q, s...)
}

func addCustomGroup(ctx *zero.Ctx) {
	gid, _ := strconv.ParseInt(ctx.State["regex_matched"].([]string)[1], 10, 64)
	mu.Lock()
	defer mu.Unlock()
	g := customGroup{ID: idof("group", strconv.FormatInt(ctx.Event.SelfID, 10)+strconv.FormatInt(gid, 10)), BotID: ctx.Event.SelfID, GroupID: gid}
	if err := db.Insert("custom_groups", &g); err != nil {
		ctx.SendChain(message.Text("ERROR: ", err))
		return
	}
	if customGroups[g.BotID] == nil {
		customGroups[g.BotID] = make(map[int64]bool)
	}
	customGroups[g.BotID][gid] = true
	ctx.SendChain(message.Text("成功"))
}

func deleteCustomGroup(ctx *zero.Ctx) {
	gid, _ := strconv.ParseInt(ctx.State["regex_matched"].([]string)[1], 10, 64)
	mu.Lock()
	defer mu.Unlock()
	if err := db.Del("custom_groups", "WHERE bot_id = ? AND group_id = ?", ctx.Event.SelfID, gid); err != nil {
		ctx.SendChain(message.Text("ERROR: ", err))
		return
	}
	delete(customGroups[ctx.Event.SelfID], gid)
	ctx.SendChain(message.Text("成功"))
}

func addCustomTrigger(ctx *zero.Ctx) {
	m := ctx.State["regex_matched"].([]string)
	t := customTrigger{ID: idof(m[1], m[2]), BotID: ctx.Event.SelfID, Trigger: m[1], Command: strings.TrimSpace(m[2])}
	mu.Lock()
	defer mu.Unlock()
	if err := db.Insert("custom_triggers", &t); err != nil {
		ctx.SendChain(message.Text("ERROR: ", err))
		return
	}
	if customTriggers[t.BotID] == nil {
		customTriggers[t.BotID] = make(map[int64]customTrigger)
	}
	customTriggers[t.BotID][t.ID] = t
	ctx.SendChain(message.Text("成功"))
}

func deleteCustomTrigger(ctx *zero.Ctx) {
	trigger := ctx.State["regex_matched"].([]string)[1]
	mu.Lock()
	defer mu.Unlock()
	if err := db.Del("custom_triggers", "WHERE bot_id = ? AND trigger = ?", ctx.Event.SelfID, trigger); err != nil {
		ctx.SendChain(message.Text("ERROR: ", err))
		return
	}
	for id, t := range customTriggers[ctx.Event.SelfID] {
		if t.Trigger == trigger {
			delete(customTriggers[ctx.Event.SelfID], id)
		}
	}
	ctx.SendChain(message.Text("成功"))
}

func listCustomGroups(ctx *zero.Ctx) {
	mu.RLock()
	defer mu.RUnlock()
	lines := []string{"[群列表]"}
	for gid := range customGroups[ctx.Event.SelfID] {
		lines = append(lines, strconv.FormatInt(gid, 10))
	}
	lines = append(lines, "[END")
	ctx.SendChain(message.Text(strings.Join(lines, "\n")))
}

func listCustomTriggers(ctx *zero.Ctx) {
	mu.RLock()
	defer mu.RUnlock()
	lines := []string{"[群触发器]"}
	for _, t := range customTriggers[ctx.Event.SelfID] {
		lines = append(lines, "\""+t.Trigger+"\" -> "+t.Command)
	}
	lines = append(lines, "[END")
	ctx.SendChain(message.Text(strings.Join(lines, "\n")))
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
				if msg == "" || msg[0] != '?' {
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
	ctx.SendChain(message.Text("您的下一条指令将被记录, 在", ctx.State["regex_matched"].([]string)[1], "时触发"))
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
