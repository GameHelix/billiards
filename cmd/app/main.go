// Command app is the Candylario app: the public pages, accounts, and
// connecting Telegram chats.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/mail"
	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/store"
	"github.com/candylario/app/internal/web"
)

const usage = `app — Candylario accounts and pages

  app serve    run the web app on APP_ADDR (default :8080)
  app users    list accounts

Needs DATABASE_URL, ENGINE_API_URL and ENGINE_API_TOKEN. Telegram needs
NOTIFIER_API_URL and NOTIFIER_API_TOKEN. APP_BASE_URL is the public address.
Email needs MAILJET_API_KEY and MAILJET_SECRET_KEY, and sends from
no-reply@APP_DOMAIN (or MAIL_FROM); without them emails go to the log.
MAILJET_WEBHOOK_SECRET opens /webhooks/mailjet for Mailjet's event reports.
ADMIN_EMAILS (comma-separated) are the admins, once their address is
confirmed.`

// commit is the source commit this binary was built from, set by the
// Dockerfile with -ldflags "-X main.commit=...".
var commit = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(out, usage)
		return nil
	}
	var missing []string
	env := map[string]string{}
	for _, k := range []string{"DATABASE_URL", "ENGINE_API_URL", "ENGINE_API_TOKEN"} {
		if env[k] = os.Getenv(k); env[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("set %s", strings.Join(missing, ", "))
	}
	if err := store.Migrate(ctx, env["DATABASE_URL"]); err != nil {
		return err
	}
	st, err := store.Open(ctx, env["DATABASE_URL"])
	if err != nil {
		return err
	}
	defer st.Close()
	engine := &api.Client{BaseURL: env["ENGINE_API_URL"], Token: env["ENGINE_API_TOKEN"], HTTP: &http.Client{Timeout: 45 * time.Second}}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, "%s %s\n", time.Now().UTC().Format("2006-01-02 15:04:05Z"), fmt.Sprintf(format, args...))
	}
	switch args[0] {
	case "serve":
		if len(args) > 1 {
			return errors.New("serve takes no arguments")
		}
		srv := &web.Server{Store: st, Engine: engine, BaseURL: os.Getenv("APP_BASE_URL"), Commit: commit, Logf: logf}
		if srv.BaseURL == "" {
			srv.BaseURL = "http://127.0.0.1:3000"
		}
		if token := os.Getenv("NOTIFIER_API_TOKEN"); token != "" {
			srv.Notifier = &notifier.Client{BaseURL: os.Getenv("NOTIFIER_API_URL"), Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}}
			if srv.Notifier.BaseURL == "" {
				srv.Notifier.BaseURL = "http://notifier:8080"
			}
		} else {
			logf("NOTIFIER_API_TOKEN is not set; Telegram is off")
		}
		if key, secret := os.Getenv("MAILJET_API_KEY"), os.Getenv("MAILJET_SECRET_KEY"); key != "" && secret != "" {
			from := os.Getenv("MAIL_FROM")
			if from == "" && os.Getenv("APP_DOMAIN") != "" {
				from = "no-reply@" + os.Getenv("APP_DOMAIN")
			}
			if from == "" {
				return errors.New("set APP_DOMAIN or MAIL_FROM to send email")
			}
			srv.Mail = &mail.Mailjet{APIKey: key, SecretKey: secret, From: from, FromName: "Candylario", HTTP: &http.Client{Timeout: 20 * time.Second}}
		} else {
			logf("MAILJET_API_KEY or MAILJET_SECRET_KEY is not set; emails go to the log")
		}
		srv.MailjetWebhookSecret = os.Getenv("MAILJET_WEBHOOK_SECRET")
		for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
			if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
				srv.Admins = append(srv.Admins, e)
			}
		}
		addr := os.Getenv("APP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute}
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			hs.Shutdown(sctx)
		}()
		logf("app listening on %s", addr)
		if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case "users":
		users, err := st.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			confirmed := "unconfirmed"
			if u.Verified() {
				confirmed = "confirmed"
			}
			fmt.Fprintf(out, "%s\t%s\ttenant %s\t%s\n", u.Email, confirmed, u.TenantID, u.CreatedAt.UTC().Format("2006-01-02"))
		}
		return nil
	}
	return fmt.Errorf("unknown command %q; run `app help`", args[0])
}
