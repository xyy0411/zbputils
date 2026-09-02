package job

import "testing"

func TestDecodeCommand(t *testing.T) {
	tests := []struct {
		name     string
		cron     string
		kind     storedKind
		matcher  string
		schedule string
		alias    string
		groupID  int64
		userID   int64
	}{
		{name: "full match", cron: "fm:hello", kind: storedFullMatch, matcher: "hello"},
		{name: "super match", cron: "sm:hello", kind: storedSuperMatch, matcher: "hello"},
		{name: "cron", cron: "0 0 * * *", kind: storedCron, matcher: "0 0 * * *", schedule: "0 0 * * *"},
		{name: "cron alias", cron: "0 0 * * *:->daily", kind: storedCron, matcher: "0 0 * * *:->daily", schedule: "0 0 * * *", alias: "daily"},
		{name: "all text regex", cron: "rm:2s:hello", kind: storedRegexAllText, matcher: "hello", groupID: 100},
		{name: "private text regex", cron: "rp:1e:2s:hello", kind: storedRegexPrivateText, matcher: "hello", groupID: 100, userID: 50},
		{name: "all inject regex", cron: "im:2s:hello", kind: storedRegexAllInject, matcher: "hello", groupID: 100},
		{name: "private inject regex", cron: "ip:1e:2s:hello", kind: storedRegexPrivateInject, matcher: "hello", groupID: 100, userID: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeStoredCmd(cmd{ID: 7, Cron: tt.cron, Cmd: "command"})
			if err != nil {
				t.Fatalf("decodeStoredCmd() error = %v", err)
			}
			if got.ID != 7 || got.Kind != tt.kind || got.Matcher != tt.matcher || got.Schedule != tt.schedule || got.Alias != tt.alias || got.GroupID != tt.groupID || got.UserID != tt.userID || got.Command != "command" {
				t.Fatalf("decodeStoredCmd() = %#v", got)
			}
		})
	}
}

func TestDecodeCommandRejectsMalformedRegex(t *testing.T) {
	for _, encoded := range []string{"rm:", "rm:not-base36:test", "rp:1:missing-group", "ip:nope:2s:test"} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := decodeStoredCmd(cmd{Cron: encoded}); err == nil {
				t.Fatalf("decodeStoredCmd(%q) succeeded", encoded)
			}
		})
	}
}
