// License: GPLv3 Copyright: 2022, Kovid Goyal, <kovid at kovidgoyal.net>

package at

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"kitty/tools/cli"
	"kitty/tools/cli/markup"
	"kitty/tools/tui/loop"
	"kitty/tools/tui/readline"
	"kitty/tools/utils"
	"kitty/tools/utils/shlex"
)

var _ = fmt.Print

var formatter *markup.Context

const prompt = "🐱 "

var ErrExec = errors.New("Execute command")

func shell_loop(rl *readline.Readline, kill_if_signaled bool) (int, error) {
	lp, err := loop.New(loop.NoAlternateScreen, loop.NoRestoreColors)
	if err != nil {
		return 1, err
	}
	rl.ChangeLoopAndResetText(lp)

	lp.OnInitialize = func() (string, error) {
		rl.Start()
		return "", nil
	}
	lp.OnFinalize = func() string { rl.End(); return "" }

	lp.OnResumeFromStop = func() error {
		rl.Start()
		return nil
	}

	lp.OnResize = rl.OnResize

	lp.OnKeyEvent = func(event *loop.KeyEvent) error {
		err := rl.OnKeyEvent(event)
		if err != nil {
			if err == io.EOF {
				lp.Quit(0)
				return nil
			}
			if err == readline.ErrAcceptInput {
				if strings.HasSuffix(rl.TextBeforeCursor(), "\\") && rl.CursorAtEndOfLine() {
					rl.OnText("\n", false, false)
					rl.Redraw()
					return nil
				}
				rl.MoveCursorToEnd()
				rl.Redraw()
				lp.ClearToEndOfScreen()
				return ErrExec
			}
			return err
		}
		if event.Handled {
			rl.Redraw()
			return nil
		}
		return nil
	}

	lp.OnText = func(text string, from_key_event, in_bracketed_paste bool) error {
		err := rl.OnText(text, from_key_event, in_bracketed_paste)
		if err == nil {
			rl.Redraw()
		}
		return err
	}

	err = lp.Run()
	if err != nil {
		return 1, err
	}
	ds := lp.DeathSignalName()
	if ds != "" {
		if kill_if_signaled {
			lp.KillIfSignalled()
			return 1, nil
		}
		return 1, fmt.Errorf("Killed by signal: %s", ds)
	}
	return 0, nil
}

func show_basic_help() {
	output := strings.Builder{}
	fmt.Fprintln(&output, "Control kitty by sending it commands.")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, formatter.Title("Commands")+":")
	r := EntryPoint(cli.NewRootCommand())
	for _, g := range r.SubCommandGroups {
		for _, sc := range g.SubCommands {
			fmt.Fprintln(&output, " ", formatter.Green(sc.Name))
			fmt.Fprintln(&output, "   ", sc.ShortDescription)
		}
	}
	fmt.Fprintln(&output, " ", formatter.Green("exit"))
	fmt.Fprintln(&output, "   ", "Exit this shell")
	cli.ShowHelpInPager(output.String())
}

func exec_command(at_root_command *cli.Command, rl *readline.Readline, cmdline string) bool {
	parsed_cmdline, err := shlex.Split(cmdline)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not parse cmdline:", err)
		return true
	}
	if len(parsed_cmdline) == 0 {
		return true
	}
	cwd, _ := os.Getwd()
	hi := readline.HistoryItem{Timestamp: time.Now(), Cmd: rl.AllText(), ExitCode: -1, Cwd: cwd}
	switch parsed_cmdline[0] {
	case "exit":
		hi.ExitCode = 0
		rl.AddHistoryItem(hi)
		return false
	case "help":
		hi.ExitCode = 0
		defer rl.AddHistoryItem(hi)
		if len(parsed_cmdline) == 1 {
			show_basic_help()
			return true
		}
		switch parsed_cmdline[1] {
		case "exit":
			fmt.Println("Exit this shell")
		case "help":
			fmt.Println("Show help")
		default:
			sc := at_root_command.FindSubCommand(parsed_cmdline[1])
			if sc == nil {
				hi.ExitCode = 1
				fmt.Fprintln(os.Stderr, "No command named", formatter.BrightRed(parsed_cmdline[1])+". Type help for a list of commands")
			} else {
				sc.ShowHelpWithCommandString(sc.Name)
			}
		}
		return true
	default:
		if at_root_command.FindSubCommand(parsed_cmdline[0]) == nil {
			hi.ExitCode = 1
			fmt.Fprintln(os.Stderr, "No command named", formatter.BrightRed(parsed_cmdline[0])+". Type help for a list of commands")
			return true
		}
		exe, err := os.Executable()
		if err != nil {
			exe, err = exec.LookPath("kitten")
			if err != nil {
				fmt.Fprintln(os.Stderr, "Could not find the kitten executable")
				return false
			}
		}
		cmdline := []string{"kitten", "@"}
		cmdline = append(cmdline, parsed_cmdline...)
		cmd := exec.Cmd{Path: exe, Args: cmdline, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
		err = cmd.Run()
		hi.Duration = time.Now().Sub(hi.Timestamp)
		hi.ExitCode = 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				hi.ExitCode = exitError.ExitCode()
			}
			fmt.Fprintln(os.Stderr, err)
		}
		rl.AddHistoryItem(hi)
	}
	return true
}

func completions(before_cursor, after_cursor string) (ans *cli.Completions) {
	const prefix = "kitten @ "
	text := prefix + before_cursor
	argv, position_of_last_arg := shlex.SplitForCompletion(text)
	if len(argv) == 0 || position_of_last_arg < len(prefix) {
		return
	}
	root := cli.NewRootCommand()
	c := root.AddSubCommand(&cli.Command{Name: "kitten"})
	EntryPoint(c)
	root.Validate()
	ans = root.GetCompletions(argv, nil)
	ans.CurrentWordIdx = position_of_last_arg - len(prefix)
	return
}

func shell_main(cmd *cli.Command, args []string) (int, error) {
	formatter = markup.New(true)
	fmt.Println("Welcome to the kitty shell!")
	fmt.Println("Use", formatter.Green("help"), "for assistance or", formatter.Green("exit"), "to quit.")
	if atwid := os.Getenv("KITTY_SHELL_ACTIVE_WINDOW_ID"); atwid != "" {
		amsg := "Previously active window id: " + atwid
		os.Unsetenv("KITTY_SHELL_ACTIVE_WINDOW_ID")
		if attid := os.Getenv("KITTY_SHELL_ACTIVE_TAB_ID"); attid != "" {
			os.Unsetenv("KITTY_SHELL_ACTIVE_TAB_ID")
			amsg += " and tab id: " + attid
		}
		fmt.Println(amsg)
	}
	rl := readline.New(nil, readline.RlInit{Prompt: prompt, Completer: completions, HistoryPath: filepath.Join(utils.CacheDir(), "shell.history.json")})
	defer func() {
		rl.Shutdown()
	}()
	for {
		rc, err := shell_loop(rl, true)
		if err != nil {
			if err == ErrExec {
				cmdline := rl.AllText()
				cmdline = strings.ReplaceAll(cmdline, "\\\n", "")
				if !exec_command(cmd, rl, cmdline) {
					return 0, nil
				}
				continue
			}
		}
		return rc, err
	}
}
