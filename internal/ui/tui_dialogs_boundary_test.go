package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func boundaryStripped(output string) string {
	return tui.StripTerminalSequences(output)
}

func assertBoundaryFramesClean(t *testing.T, output string, visible []string, forbidden []string) {
	t.Helper()
	stripped := boundaryStripped(output)
	if strings.Contains(stripped, "\x1b") {
		t.Fatalf("stripped frames leaked escape:\n%q", stripped)
	}
	for _, r := range stripped {
		if r < 0x20 && r != '\n' && r != '\r' {
			t.Fatalf("stripped frames leaked C0 %U:\n%q", r, stripped)
		}
		if r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("stripped frames leaked control %U:\n%q", r, stripped)
		}
	}
	for _, bad := range forbidden {
		if bad != "" && strings.Contains(stripped, bad) {
			t.Fatalf("stripped frames leaked payload %q:\n%s", bad, stripped)
		}
	}
	for _, want := range visible {
		if want != "" && !strings.Contains(stripped, want) {
			t.Fatalf("frames missing visible %q:\n%s", want, stripped)
		}
	}
}

func TestBoundaryConfirmHostileUI(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		message   string
		visible   []string
		forbidden []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE_T", message: "M\x1b[31mVISIBLE_M\x1b[0m", visible: []string{"VISIBLE_T", "VISIBLE_M"}, forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_UI_T_1\x07VISIBLE_T", message: "M\x1b]0;OSC_EVIL_UI_M_1\x07VISIBLE_M", visible: []string{"VISIBLE_T", "VISIBLE_M"}, forbidden: []string{"OSC_EVIL_UI_T_1", "OSC_EVIL_UI_M_1"}},
		{name: "apc", title: "T\x1b_APP_UI_T_1\x1b\\VISIBLE_T", message: "M\x1b_APP_UI_M_1\x1b\\VISIBLE_M", visible: []string{"VISIBLE_T", "VISIBLE_M"}, forbidden: []string{"APP_UI_T_1", "APP_UI_M_1"}},
		{name: "dcs", title: "T\x1bPqDCS_UI_T_1\x1b\\VISIBLE_T", message: "M\x1bPqDCS_UI_M_1\x1b\\VISIBLE_M", visible: []string{"VISIBLE_T", "VISIBLE_M"}, forbidden: []string{"DCS_UI_T_1", "DCS_UI_M_1"}},
		{name: "c0", title: "T\x00VISIBLE_T", message: "M\x07VISIBLE_M", visible: []string{"VISIBLE_T", "VISIBLE_M"}, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner, terminal := startTestRunner(t, TUIModeRegular, nil)
			done := make(chan bool, 1)
			go func() {
				ok, _ := runner.Confirm(tc.title, tc.message)
				done <- ok
			}()
			waitForDialog(t, runner)
			runner.view.RenderNow(true)
			assertBoundaryFramesClean(t, terminal.Output(), tc.visible, tc.forbidden)
			terminal.SendInput("y")
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("confirm did not resolve")
			}
		})
	}
}

func TestBoundarySelectHostileUI(t *testing.T) {
	cases := []struct {
		name      string
		option    string
		visible   string
		forbidden []string
	}{
		{name: "csi", option: "\x1b[2JVISIBLE_OPT", visible: "VISIBLE_OPT", forbidden: []string{"[2J"}},
		{name: "osc", option: "\x1b]0;OSC_EVIL_OPT_2\x07VISIBLE_OPT", visible: "VISIBLE_OPT", forbidden: []string{"OSC_EVIL_OPT_2"}},
		{name: "apc", option: "\x1b_APP_OPT_2\x1b\\VISIBLE_OPT", visible: "VISIBLE_OPT", forbidden: []string{"APP_OPT_2"}},
		{name: "dcs", option: "\x1bPqDCS_OPT_2\x1b\\VISIBLE_OPT", visible: "VISIBLE_OPT", forbidden: []string{"DCS_OPT_2"}},
		{name: "c0", option: "a\x00VISIBLE_OPT", visible: "VISIBLE_OPT", forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner, terminal := startTestRunner(t, TUIModeRegular, nil)
			done := make(chan string, 1)
			go func() {
				v, _ := runner.Select("T\x1b[2JVISIBLE_T", []string{tc.option, "other"})
				done <- v
			}()
			waitForDialog(t, runner)
			runner.view.RenderNow(true)
			assertBoundaryFramesClean(t, terminal.Output(), []string{tc.visible, "VISIBLE_T"}, tc.forbidden)
			terminal.SendInput("\r")
			select {
			case got := <-done:
				if got != tc.option {
					t.Fatalf("select = %q, want exact raw %q", got, tc.option)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("select did not resolve")
			}
		})
	}
}

func TestBoundaryInputEditorHostileUI(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		placeholder string
		forbidden   []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE_T", placeholder: "P\x1b[31mVISIBLE_PH", forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_T_3\x07VISIBLE_T", placeholder: "P\x1b]0;OSC_EVIL_PH_3\x07VISIBLE_PH", forbidden: []string{"OSC_EVIL_T_3", "OSC_EVIL_PH_3"}},
		{name: "apc", title: "T\x1b_APP_T_3\x1b\\VISIBLE_T", placeholder: "P\x1b_APP_PH_3\x1b\\VISIBLE_PH", forbidden: []string{"APP_T_3", "APP_PH_3"}},
		{name: "dcs", title: "T\x1bPqDCS_T_3\x1b\\VISIBLE_T", placeholder: "P\x1bPqDCS_PH_3\x1b\\VISIBLE_PH", forbidden: []string{"DCS_T_3", "DCS_PH_3"}},
	}
	for _, tc := range cases {
		t.Run("input-"+tc.name, func(t *testing.T) {
			runner, terminal := startTestRunner(t, TUIModeRegular, nil)
			done := make(chan string, 1)
			go func() {
				v, _ := runner.Input(tc.title, tc.placeholder)
				done <- v
			}()
			waitForDialog(t, runner)
			runner.view.RenderNow(true)
			assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_T", "VISIBLE_PH"}, tc.forbidden)
			terminal.SendInput("ok")
			terminal.SendInput("\r")
			select {
			case got := <-done:
				if got != "ok" {
					t.Fatalf("input = %q, want ok", got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("input did not resolve")
			}
		})
		t.Run("editor-"+tc.name, func(t *testing.T) {
			runner, terminal := startTestRunner(t, TUIModeRegular, nil)
			done := make(chan string, 1)
			prefill := "  ordinary"
			go func() {
				v, _ := runner.Editor(tc.title, prefill)
				done <- v
			}()
			waitForDialog(t, runner)
			runner.view.RenderNow(true)
			assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_T"}, tc.forbidden)
			terminal.SendInput("\r")
			select {
			case got := <-done:
				if got != prefill {
					t.Fatalf("editor = %q, want exact %q", got, prefill)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("editor did not resolve")
			}
		})
	}
}

func TestBoundaryModelHelpSessionTrustOAuthHostileUI(t *testing.T) {
	t.Run("model", func(t *testing.T) {
		cases := []struct {
			name      string
			provider  string
			id        string
			forbidden []string
		}{
			{name: "csi", provider: "P\x1b[2JVISIBLE_PROV", id: "raw-model-csi", forbidden: []string{"[2J"}},
			{name: "osc", provider: "P\x1b]0;OSC_EVIL_PROV_4\x07VISIBLE_PROV", id: "raw-model-osc", forbidden: []string{"OSC_EVIL_PROV_4"}},
			{name: "apc", provider: "P\x1b_APP_PROV_4\x1b\\VISIBLE_PROV", id: "raw-model-apc", forbidden: []string{"APP_PROV_4"}},
			{name: "dcs", provider: "P\x1bPqDCS_PROV_4\x1b\\VISIBLE_PROV", id: "raw-model-dcs", forbidden: []string{"DCS_PROV_4"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				done := make(chan string, 1)
				go func() {
					v, _, _ := runner.SelectModel(context.Background(), tc.id, []ModelChoice{{ID: tc.id, Provider: tc.provider, ContextWindow: 1000}})
					done <- v
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_PROV"}, tc.forbidden)
				terminal.SendInput("\r")
				select {
				case got := <-done:
					if got != tc.id {
						t.Fatalf("model = %q, want exact %q", got, tc.id)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("model did not resolve")
				}
			})
		}
	})
	t.Run("help", func(t *testing.T) {
		cases := []struct {
			name      string
			desc      string
			forbidden []string
		}{
			{name: "csi", desc: "D\x1b[2JVISIBLE_HELP", forbidden: []string{"[2J"}},
			{name: "osc", desc: "D\x1b]0;OSC_EVIL_HELP_5\x07VISIBLE_HELP", forbidden: []string{"OSC_EVIL_HELP_5"}},
			{name: "apc", desc: "D\x1b_APP_HELP_5\x1b\\VISIBLE_HELP", forbidden: []string{"APP_HELP_5"}},
			{name: "dcs", desc: "D\x1bPqDCS_HELP_5\x1b\\VISIBLE_HELP", forbidden: []string{"DCS_HELP_5"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				done := make(chan error, 1)
				go func() {
					done <- runner.ShowHelp(context.Background(), []HelpEntry{{Name: "cmd", Description: tc.desc}})
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_HELP"}, tc.forbidden)
				terminal.SendInput("\r")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("help did not resolve")
				}
			})
		}
	})
	t.Run("session", func(t *testing.T) {
		cases := []struct {
			name      string
			label     string
			forbidden []string
		}{
			{name: "csi", label: "L\x1b[2JVISIBLE_SESS", forbidden: []string{"[2J"}},
			{name: "osc", label: "L\x1b]0;OSC_EVIL_SESS_6\x07VISIBLE_SESS", forbidden: []string{"OSC_EVIL_SESS_6"}},
			{name: "apc", label: "L\x1b_APP_SESS_6\x1b\\VISIBLE_SESS", forbidden: []string{"APP_SESS_6"}},
			{name: "dcs", label: "L\x1bPqDCS_SESS_6\x1b\\VISIBLE_SESS", forbidden: []string{"DCS_SESS_6"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				path := "/tmp/session-" + tc.name + ".jsonl"
				done := make(chan string, 1)
				go func() {
					v, _, _ := runner.SelectSession(context.Background(), []SessionChoice{{Path: path, Label: tc.label, Description: "D\x1b[2JVISIBLE_D"}})
					done <- v
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_SESS"}, tc.forbidden)
				terminal.SendInput("\r")
				select {
				case got := <-done:
					if got != path {
						t.Fatalf("session = %q, want exact %q", got, path)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("session did not resolve")
				}
			})
		}
	})
	t.Run("trust", func(t *testing.T) {
		cases := []struct {
			name      string
			workspace string
			forbidden []string
		}{
			{name: "csi", workspace: "/work/\x1b[2JVISIBLE_WS", forbidden: []string{"[2J"}},
			{name: "osc", workspace: "/work/\x1b]0;OSC_EVIL_WS_7\x07VISIBLE_WS", forbidden: []string{"OSC_EVIL_WS_7"}},
			{name: "apc", workspace: "/work/\x1b_APP_WS_7\x1b\\VISIBLE_WS", forbidden: []string{"APP_WS_7"}},
			{name: "dcs", workspace: "/work/\x1bPqDCS_WS_7\x1b\\VISIBLE_WS", forbidden: []string{"DCS_WS_7"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				done := make(chan bool, 1)
				go func() {
					ok, _ := runner.ConfirmTrust(context.Background(), tc.workspace)
					done <- ok
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_WS"}, tc.forbidden)
				terminal.SendInput("y")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("trust did not resolve")
				}
			})
		}
	})
	t.Run("oauth", func(t *testing.T) {
		cases := []struct {
			name      string
			provider  string
			url       string
			code      string
			forbidden []string
		}{
			{name: "csi", provider: "P\x1b[2JVISIBLE_PROV", url: "https://example.test/\x1b[2JVISIBLE_URL", code: "C\x1b[31mVISIBLE_CODE", forbidden: []string{"[2J"}},
			{name: "osc", provider: "P\x1b]0;OSC_EVIL_OAUTH_8\x07VISIBLE_PROV", url: "https://example.test/VISIBLE_URL", code: "VISIBLE_CODE", forbidden: []string{"OSC_EVIL_OAUTH_8"}},
			{name: "apc", provider: "P\x1b_APP_OAUTH_8\x1b\\VISIBLE_PROV", url: "https://example.test/VISIBLE_URL", code: "VISIBLE_CODE", forbidden: []string{"APP_OAUTH_8"}},
			{name: "dcs", provider: "P\x1bPqDCS_OAUTH_8\x1b\\VISIBLE_PROV", url: "https://example.test/VISIBLE_URL", code: "VISIBLE_CODE", forbidden: []string{"DCS_OAUTH_8"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				done := make(chan bool, 1)
				go func() {
					ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{Title: "T\x1b[2JVISIBLE_T", Provider: tc.provider, VerificationURL: tc.url, UserCode: tc.code, State: "S\x1b[2JVISIBLE_S"})
					done <- ok
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_PROV", "VISIBLE_URL", "VISIBLE_CODE"}, tc.forbidden)
				terminal.SendInput("\x1b")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("oauth did not resolve")
				}
			})
		}
	})
	t.Run("settings", func(t *testing.T) {
		cases := []struct {
			name      string
			label     string
			desc      string
			forbidden []string
		}{
			{name: "csi", label: "L\x1b[2JVISIBLE_L", desc: "D\x1b[2JVISIBLE_D", forbidden: []string{"[2J"}},
			{name: "osc", label: "L\x1b]0;OSC_EVIL_SET_9\x07VISIBLE_L", desc: "D\x1b]0;OSC_EVIL_SET_D_9\x07VISIBLE_D", forbidden: []string{"OSC_EVIL_SET_9", "OSC_EVIL_SET_D_9"}},
			{name: "apc", label: "L\x1b_APP_SET_9\x1b\\VISIBLE_L", desc: "D\x1b_APP_SET_D_9\x1b\\VISIBLE_D", forbidden: []string{"APP_SET_9", "APP_SET_D_9"}},
			{name: "dcs", label: "L\x1bPqDCS_SET_9\x1b\\VISIBLE_L", desc: "D\x1bPqDCS_SET_D_9\x1b\\VISIBLE_D", forbidden: []string{"DCS_SET_9", "DCS_SET_D_9"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner, terminal := startTestRunner(t, TUIModeRegular, nil)
				done := make(chan bool, 1)
				go func() {
					_, ok, _ := runner.ShowSettings(context.Background(), []tui.SettingItem{{ID: "k", Label: tc.label, Description: tc.desc, CurrentValue: "on", Values: []string{"on", "off"}}})
					done <- ok
				}()
				waitForDialog(t, runner)
				runner.view.RenderNow(true)
				assertBoundaryFramesClean(t, terminal.Output(), []string{"VISIBLE_L", "VISIBLE_D"}, tc.forbidden)
				terminal.SendInput("\x1b")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("settings did not resolve")
				}
			})
		}
	})
}
