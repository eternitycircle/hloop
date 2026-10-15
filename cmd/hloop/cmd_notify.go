package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"hloop/internal/notify"
	"hloop/internal/sched"
	"hloop/internal/store"
)

func cmdNotify(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop notify setup ntfy|telegram | test | status | off | listen | service install|remove")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "setup":
		if len(rest) == 0 {
			return fmt.Errorf("usage: hloop notify setup ntfy|telegram")
		}
		switch rest[0] {
		case "ntfy":
			return setupNtfy(rest[1:])
		case "telegram":
			return setupTelegram(rest[1:])
		}
		return fmt.Errorf("unknown channel %q (ntfy or telegram)", rest[0])
	case "test":
		g, _ := store.LoadGlobal()
		d := notify.NewDispatcher(os.Stderr)
		d.G = g
		ch := d.Channels()
		if len(ch) == 0 {
			return fmt.Errorf("nothing configured yet: hloop notify setup ntfy|telegram")
		}
		d.Send(notify.Event{Kind: notify.KindComplete, Title: "hloop test", Body: "If you can read this on your phone, notifications work.", Priority: 3})
		fmt.Println("Sent a test notification via:", strings.Join(ch, ", "))
		return nil
	case "status":
		g, _ := store.LoadGlobal()
		if g.Ntfy.Topic != "" {
			srv := g.Ntfy.Server
			if srv == "" {
				srv = "https://ntfy.sh"
			}
			fmt.Printf("ntfy: on  (server %s, topic %s)\n", srv, g.Ntfy.Topic)
		} else {
			fmt.Println("ntfy: off")
		}
		if g.Telegram.Token != "" && g.Telegram.ChatID != 0 {
			fmt.Printf("telegram: on (bot @%s, paired chat %d)\n", g.Telegram.BotUsername, g.Telegram.ChatID)
		} else {
			fmt.Println("telegram: off")
		}
		fmt.Printf("desktop alerts: %v\n", !g.NoDesktop)
		fmt.Println("service:", map[bool]string{true: "installed", false: "not installed"}[sched.Installed(sched.Spec{Kind: "listen"})])
		return nil
	case "off":
		which := "all"
		if len(rest) > 0 {
			which = rest[0]
		}
		g, _ := store.LoadGlobal()
		switch which {
		case "ntfy":
			g.Ntfy = store.NtfyCfg{}
		case "telegram":
			g.Telegram = store.TelegramCfg{}
		case "desktop":
			g.NoDesktop = true
		case "all":
			g.Ntfy, g.Telegram = store.NtfyCfg{}, store.TelegramCfg{}
		default:
			return fmt.Errorf("usage: hloop notify off [ntfy|telegram|desktop|all]")
		}
		if err := store.SaveGlobal(g); err != nil {
			return err
		}
		fmt.Println("Turned off:", which)
		return nil
	case "listen":
		g, _ := store.LoadGlobal()
		if g.Telegram.Token == "" || g.Telegram.ChatID == 0 {
			return fmt.Errorf("telegram is not set up: hloop notify setup telegram")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return notify.NewBot(g, exePath(), os.Stdout).Listen(ctx)
	case "service":
		if len(rest) == 0 || (rest[0] != "install" && rest[0] != "remove") {
			return fmt.Errorf("usage: hloop notify service install|remove")
		}
		spec := sched.Spec{Kind: "listen", Exe: exePath()}
		if rest[0] == "remove" {
			if err := sched.Remove(spec); err != nil {
				return err
			}
			fmt.Println("Chat listener service removed.")
			return nil
		}
		g, _ := store.LoadGlobal()
		if g.Telegram.Token == "" {
			return fmt.Errorf("telegram is not set up yet: hloop notify setup telegram")
		}
		desc, err := sched.Install(spec)
		if err != nil {
			return err
		}
		fmt.Println("Installed:", desc)
		fmt.Println("The chat listener starts at login/boot. Start it now with: hloop notify listen")
		return nil
	}
	return fmt.Errorf("unknown notify command %q", sub)
}

func setupNtfy(args []string) error {
	fs := newFS("notify setup ntfy")
	server := fs.String("server", "", "ntfy server (default https://ntfy.sh)")
	topic := fs.String("topic", "", "topic name (default: a random unguessable one)")
	token := fs.String("token", "", "access token for a protected server")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *server != "" {
		u, err := url.Parse(*server)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return fmt.Errorf("--server must be a full URL such as https://ntfy.example.com")
		}
		if u.Scheme == "http" {
			host := u.Hostname()
			local := host == "localhost" || host == "127.0.0.1" || host == "::1"
			if *token != "" && !local {
				return fmt.Errorf("refusing to send an access token over plain http; use https")
			}
			fmt.Fprintln(os.Stderr, "warning: plain http sends your alerts unencrypted; use https unless this server is on a trusted network")
		}
	}
	if *topic != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(*topic) {
		return fmt.Errorf("--topic may only contain letters, digits, - and _ (up to 64 characters)")
	}
	g, _ := store.LoadGlobal()
	g.Ntfy.Server = strings.TrimRight(*server, "/")
	g.Ntfy.Topic = *topic
	if g.Ntfy.Topic == "" {
		g.Ntfy.Topic = notify.NewTopic()
	}
	g.Ntfy.Token = *token
	if err := store.SaveGlobal(g); err != nil {
		return err
	}
	srv := g.Ntfy.Server
	if srv == "" {
		srv = "https://ntfy.sh"
	}
	fmt.Println("ntfy alerts enabled (one-way: your computer -> your phone).")
	fmt.Println("On your phone: install the free ntfy app (iOS/Android), tap +, and subscribe to:")
	fmt.Printf("\n    server: %s\n    topic:  %s\n\n", srv, g.Ntfy.Topic)
	fmt.Println("On the public server the topic name is the only protection: treat it like a password.")
	fmt.Println("Alerts contain task titles and blocker text and pass through the ntfy server in readable form;")
	fmt.Println("for anything sensitive, self-host ntfy (--server https://... --token ...) or use Telegram.")
	d := notify.NewDispatcher(os.Stderr)
	d.Send(notify.Event{Kind: notify.KindComplete, Title: "hloop connected", Body: "Notifications are working."})
	fmt.Println("A test notification was sent.")
	return nil
}

func setupTelegram(args []string) error {
	fs := newFS("notify setup telegram")
	token := fs.String("token", "", "bot token from @BotFather (omit to be prompted; avoids shell history)")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	tok := strings.TrimSpace(*token)
	r := bufio.NewReader(os.Stdin)
	if tok == "" {
		fmt.Println("Two-way phone control uses a private Telegram bot that only you can talk to.")
		fmt.Println()
		fmt.Println("1. In Telegram, open a chat with @BotFather and send /newbot")
		fmt.Println("2. Choose a name and a username ending in 'bot'")
		fmt.Println("3. Paste the token BotFather gives you below")
		fmt.Println()
		tok = prompt(r, "Bot token", "")
	}
	if tok == "" {
		return fmt.Errorf("no token given")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	tg := notify.NewTG(tok)
	me, err := tg.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("that token was not accepted: %v", err)
	}
	code := notify.NewPairCode()
	fmt.Printf("\nBot found: @%s\n\n", me.Username)
	fmt.Println("Now pair your phone. Either tap this link on your phone:")
	fmt.Printf("\n    https://t.me/%s?start=%s\n\n", me.Username, code)
	fmt.Printf("or open @%s and send:\n\n    /pair %s\n\n", me.Username, code)
	fmt.Println("Waiting up to 5 minutes (stop `hloop notify listen` first if it is running)...")
	user, chat, offset, err := tg.Pair(ctx, code, 5*time.Minute)
	if err != nil {
		return err
	}
	g, _ := store.LoadGlobal()
	g.Telegram = store.TelegramCfg{Token: tok, BotUsername: me.Username, UserID: user.ID, ChatID: chat.ID}
	if err := store.SaveGlobal(g); err != nil {
		return err
	}
	_ = store.UpdateTGState(func(s *store.TGState) {
		if offset > s.Offset {
			s.Offset = offset
		}
	})
	_, _ = tg.SendMessage(ctx, chat.ID, "Paired. I'll message you when the loop needs you. Send /help to see what you can do from here.", false, nil)
	fmt.Printf("Paired with %q. Only this Telegram account can control your projects.\n\n", orNone(user.Username))
	fmt.Println("Alerts are on. To also accept commands from your phone, keep the listener running:")
	fmt.Println("    hloop notify listen            (foreground)")
	fmt.Println("    hloop notify service install   (start automatically at login/boot)")
	return nil
}

// ---------- schedule ----------

func cmdSchedule(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop schedule install [--every 8] [--no-boot] | remove | status")
	}
	sub, rest := args[0], args[1:]
	fs := newFS("schedule " + sub)
	dir := fs.String("C", "", "project directory or name")
	every := fs.Int("every", 8, "run every N hours")
	noBoot := fs.Bool("no-boot", false, "do not also run at login/boot")
	if _, err := parseArgs(fs, rest); err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	name := projectName(p, cfg)
	spec := sched.Spec{Kind: "run", Name: name, Root: p.Root, Exe: exePath(), EveryHours: *every, AtBoot: !*noBoot}
	switch sub {
	case "install":
		if *every < 1 || *every > 24 {
			return fmt.Errorf("--every must be between 1 and 24 hours")
		}
		desc, err := sched.Install(spec)
		if err != nil {
			return err
		}
		fmt.Printf("Scheduled %q every %d hour(s)%s.\n  %s\n", name, *every, map[bool]string{true: " and at login/boot", false: ""}[!*noBoot], desc)
		fmt.Println("Runs skip themselves while a quota backoff or a blocker is active, so a short interval is safe.")
		return nil
	case "remove":
		if err := sched.Remove(spec); err != nil {
			return err
		}
		fmt.Println("Schedule removed.")
		return nil
	case "status":
		fmt.Println("scheduled:", sched.Installed(spec))
		return nil
	}
	return fmt.Errorf("unknown schedule command %q", sub)
}
