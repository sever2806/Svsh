package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/abiosoft/ishell"
	"github.com/abiosoft/readline"
	"github.com/alecthomas/kong"
	"github.com/fatih/color"

	"github.com/ido50/svsh/pkg/svsh"
)

var cli struct {
	Suite    string `short:"s" help:"The supervision suite managing the base directory (perp, s6 or runit)"`
	Basedir  string `optional:"" short:"d" help:"Service directory (directory on which the supervisor was started)"`
	Bindir   string `optional:"" short:"b" help:"Directory where the supervisor is installed (e.g. /usr/sbin)"`
	Collapse bool   `optional:"" short:"c" help:"Collapse numbered services into one line"`
	Debug    bool   `optional:"" help:"Enable debug mode"`

	Status struct {
	} `cmd:"" default:"1" help:"List all processes and their statuses"`

	Start struct {
		Services []string `arg:"" help:"Names of services to start"`
	} `cmd:"" help:"Start one or more services"`

	Stop struct {
		Services []string `arg:"" help:"Names of services to stop"`
	} `cmd:"" help:"Stop one or more services"`

	Restart struct {
		Services []string `arg:"" help:"Names of services to restart"`
	} `cmd:"" help:"Restart one or more services"`

	Signal struct {
		Signal   string   `arg:"" help:"Name of signal to send"`
		Services []string `arg:"" help:"Names of services to signal"`
	} `cmd:"" help:"Send a signal to one or more services"`

	Fg struct {
		Service string `arg:"" help:"Name of service to move to foreground"`
	} `cmd:"" help:"View the logs of a service in real-time"`

	Rescan struct {
	} `cmd:"" help:"Rescan the service directory to look for new/removed services"`

	Terminate struct {
	} `cmd:"" help:"Terminate the supervisor (all services will terminate)"`

	Version struct{} `cmd:"" help:"Print version information and exit"`
}

type context struct {
	suite svsh.Supervisor
	k     *kong.Context
}

func main() {
	ctx := new(context)
	ctx.k = kong.Parse(
		&cli,
		kong.Name("svsh"),
		kong.Description("Process supervision shell"),
		kong.UsageOnError(),
	)

	switch cli.Suite {
	case "runit":
		ctx.suite = &svsh.Runit{
			BaseDir:   cli.Basedir,
			DebugMode: cli.Debug,
		}
	default:
		fmt.Fprintf(os.Stderr, "Invalid supervisor %q\n", cli.Suite)
		ctx.k.Exit(1)
	}

	shell := ishell.NewWithConfig(&readline.Config{Prompt: "svsh> "})

	shell.AddCmd(&ishell.Cmd{
		Name: "status",
		Help: "List all processes and their statuses",
		Func: ctx.status,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:                "start",
		Help:                "Start one or more services",
		Func:                ctx.start,
		CompleterWithPrefix: ctx.autoCompleteService,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:                "stop",
		Help:                "Stop one or more services",
		Func:                ctx.stop,
		CompleterWithPrefix: ctx.autoCompleteService,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:                "restart",
		Help:                "Restart one or more services",
		Func:                ctx.restart,
		CompleterWithPrefix: ctx.autoCompleteService,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:                "signal",
		Help:                "Send a signal to a service",
		Aliases:             []string{"sig"},
		Func:                ctx.signal,
		CompleterWithPrefix: ctx.autoCompleteSignal,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:                "fg",
		Help:                "View the logs of a service in real-time",
		Func:                ctx.fg,
		CompleterWithPrefix: ctx.autoCompleteService,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:    "rescan",
		Aliases: []string{"update"},
		Help:    "Rescan the service directory to look for new/removed services",
		Func:    ctx.rescan,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:    "terminate",
		Aliases: []string{"shutdown"},
		Help:    "Terminate the supervisor (all services will terminate)",
		Func:    ctx.terminate,
	})

	shell.AddCmd(&ishell.Cmd{
		Name: "toggle",
		Help: "Toggle Svsh switches (e.g. collapse)",
		Func: ctx.nop,
	})

	shell.AddCmd(&ishell.Cmd{
		Name:    "quit",
		Aliases: []string{"exit"},
		Help:    "Quit the shell",
		Func: func(c *ishell.Context) {
			ctx.k.Exit(0)
		},
	})

	// we will only start the shell if we didn't get any specific command
	var (
		startShell bool
		err        error
	)

	switch ctx.k.Command() {
	case "status":
		var found bool

		for _, arg := range ctx.k.Args {
			if arg == "status" {
				found = true
				break
			}
		}

		if !found {
			startShell = true
		}

		err = shell.Process("status")
	case "start <services>":
		args := append([]string{"start"}, cli.Start.Services...)
		err = shell.Process(args...)
	case "stop <services>":
		args := append([]string{"stop"}, cli.Stop.Services...)
		err = shell.Process(args...)
	case "restart <services>":
		args := append([]string{"restart"}, cli.Restart.Services...)
		err = shell.Process(args...)
	case "signal <signal> <services>":
		args := append([]string{"signal"}, cli.Signal.Signal)
		args = append(args, cli.Signal.Services...)
		err = shell.Process(args...)
	case "fg <service>":
		err = shell.Process("fg", cli.Fg.Service)
	case "rescan", "terminate":
		err = shell.Process(ctx.k.Command())
	case "version":
		fmt.Printf("svsh version %s\n", svsh.Version)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		ctx.k.Exit(1)
	}

	// are we running the shell?
	if startShell {
		shell.Run()
	}
}

func (ctx *context) status(c *ishell.Context) {
	svcs, err := ctx.suite.Status()
	if err != nil {
		c.Printf("Failed reading statuses: %s\n", err)
		ctx.k.Exit(1)
	}

	header := color.New(color.FgBlack).
		Add(color.BgWhite).
		Add(color.Bold).
		SprintfFunc()
	c.Println(header("%16s | %10s | %8s | %5s", "process", "status", "duration", "pid"))

	for _, svc := range svcs {
		var fn func(string, ...interface{}) string

		switch svc.Status {
		case svsh.StatusUp:
			fn = color.New(color.FgGreen).Add(color.Bold).SprintfFunc()
		case svsh.StatusResetting:
			fn = color.New(color.FgYellow).Add(color.Bold).SprintfFunc()
		default:
			fn = color.New(color.FgRed).Add(color.Bold).SprintfFunc()
		}

		c.Printf("%16s |", svc.Name)
		c.Printf(fn("%10s", svc.Status))
		c.Printf(" | %8s | %5d\n", svc.Duration, svc.Pid)
	}
}

func (ctx *context) start(c *ishell.Context) {
	err := ctx.suite.Start(c.Args...)
	if err != nil {
		c.Println(err)
		ctx.k.Exit(1)
	}
}

func (ctx *context) stop(c *ishell.Context) {
	err := ctx.suite.Stop(c.Args...)
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) restart(c *ishell.Context) {
	err := ctx.suite.Restart(c.Args...)
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) signal(c *ishell.Context) {
	sig, err := parseSignal(c.Args[0])
	if err != nil {
		c.Printf("Error: %s\n", err)
		return
	}

	err = ctx.suite.Signal(sig, c.Args[1:]...)
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) fg(c *ishell.Context) {
	err := ctx.suite.Fg(c.Args[0])
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) rescan(c *ishell.Context) {
	err := ctx.suite.Rescan()
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) terminate(c *ishell.Context) {
	err := ctx.suite.Terminate()
	if err != nil {
		c.Printf("Error: %s\n", err)
	}
}

func (ctx *context) nop(c *ishell.Context) {
}

func parseSignal(s string) (sig os.Signal, err error) {
	var ok bool
	sig, ok = signals[strings.ToLower(s)]

	if !ok {
		sig, ok = signals[strings.ToUpper(s)]

		if !ok {
			err = svsh.ErrUnsupportedSignal
		}
	}

	return sig, err
}

var signals = map[string]os.Signal{
	"hup":   syscall.SIGHUP,
	"int":   syscall.SIGINT,
	"quit":  syscall.SIGQUIT,
	"kill":  syscall.SIGKILL,
	"usr1":  syscall.SIGUSR1,
	"usr2":  syscall.SIGUSR2,
	"alrm":  syscall.SIGALRM,
	"term":  syscall.SIGTERM,
	"cont":  syscall.SIGCONT,
	"winch": syscall.SIGWINCH,
}

func (ctx *context) autoCompleteService(s string, _ []string) []string {
	svcs, err := ctx.suite.Status()
	if err != nil {
		return nil
	}

	matches := make([]string, 0, len(svcs))

	for _, svc := range svcs {
		if s == "" || strings.HasPrefix(
			strings.ToLower(svc.Name),
			strings.ToLower(s),
		) {
			matches = append(matches, svc.Name)
		}
	}

	return matches
}

func (ctx *context) autoCompleteSignal(s string, args []string) []string {
	if len(args) > 1 {
		// autocomplete on service names
		return ctx.autoCompleteService(s, args)
	}

	// autocomplete on signal name
	matches := make([]string, 0, len(signals))

	for sig := range signals {
		if s == "" || strings.HasPrefix(sig, strings.ToLower(s)) {
			matches = append(matches, sig)
		}
	}

	return matches
}
