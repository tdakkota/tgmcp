// Command tgmcp is a Model Context Protocol (MCP) server backed by the gotd
// Telegram client. It exposes tools to list channels with unread messages and
// to read those unread messages.
//
// Usage:
//
//	tgmcp auth     # one-time interactive login, stores the session
//	tgmcp serve    # run the MCP server over HTTP
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-faster/errors"
	"github.com/go-faster/sdk/app"
	"github.com/gotd/contrib/bbolt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// modulePath is reported as the version of this build by [app.Run].
const modulePath = "github.com/gotd/tgmcp"

func main() {
	if err := rootCmd().ExecuteContext(context.Background()); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %+v\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tgmcp",
		Short:         "MCP server for reading unread Telegram channel messages",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		&cobra.Command{
			Use:   "auth",
			Short: "Interactively log in and store the Telegram session",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}

				// Interactive command: no telemetry, own signal handling.
				ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer cancel()

				return runAuth(ctx, cfg)
			},
		},
		&cobra.Command{
			Use:   "echobot",
			Short: "Run only the inline echo bot used for agent attribution",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}

				logCfg, err := zapConfig(cfg)
				if err != nil {
					return err
				}

				app.Run(func(ctx context.Context, lg *zap.Logger, _ *app.Telemetry) error {
					return runEchoBot(ctx, cfg, lg)
				},
					app.WithContext(cmd.Context()),
					app.WithServiceName("tgmcp-echobot"),
					app.WithModulePath(modulePath),
					app.WithZapConfig(logCfg),
				)

				return nil
			},
		},
		&cobra.Command{
			Use:   "serve",
			Short: "Run the MCP server over HTTP",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}

				logCfg, err := zapConfig(cfg)
				if err != nil {
					return err
				}

				// app.Run owns signal handling, graceful shutdown and process
				// exit, so it never returns.
				app.Run(func(ctx context.Context, lg *zap.Logger, t *app.Telemetry) error {
					return runServe(ctx, cfg, lg, t)
				},
					app.WithContext(cmd.Context()),
					app.WithServiceName("tgmcp"),
					app.WithModulePath(modulePath),
					app.WithZapConfig(logCfg),
				)

				return nil
			},
		},
	)

	return root
}

// runServe connects to Telegram using the stored session and serves the MCP
// protocol over HTTP using the streamable transport. It never prompts: if the
// session is missing or expired, it asks the user to run "tgmcp auth" first.
//
// The dialog list is loaded once at startup into an in-memory cache, which is
// then kept live by the gotd updates manager (gap-safe via getDifference). This
// avoids re-fetching the dialog list on every tool call, which caused
// FLOOD_WAIT.
func runServe(ctx context.Context, cfg Config, lg *zap.Logger, t *app.Telemetry) error {
	db, err := openStateDB(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			lg.Error("Close state database", zap.Error(err))
		}
	}()

	cache := newDialogCache(&dialogStore{db: db}, lg)
	msgs := &messageStore{db: db, cap: messageBufferCap}

	dispatcher := tg.NewUpdateDispatcher()
	registerCacheHandlers(&dispatcher, cache, msgs, lg.Named("update"))

	// api is set once the client is connected; the OnChannelTooLong callback
	// (invoked later, from the updates manager) refetches the affected channel.
	var api *tg.Client
	mgr := updates.New(updates.Config{
		Handler:      dispatcher,
		Storage:      bbolt.NewStateStorage(db),
		AccessHasher: accessHasher{db: db},
		OnChannelTooLong: func(channelID int64) {
			if api == nil {
				return
			}
			// Refresh asynchronously so we do not block update processing.
			go func() {
				if err := refreshChannel(ctx, api, cache, channelID); err != nil {
					lg.Warn("Refresh channel after difference too long",
						zap.Int64("channel_id", channelID), zap.Error(err))
				}
			}()
		},
		OnChannelInaccessible: func(channelID int64) {
			// The updates manager lost access to the channel (CHANNEL_PRIVATE)
			// and dropped it from tracking; drop it from the dialog cache too.
			cache.remove(channelID)
			lg.Info("Dropped inaccessible channel from cache",
				zap.Int64("channel_id", channelID))
		},
		OnTooLong: func() {
			if api == nil {
				return
			}
			// Account-wide gap too long to recover: re-load the whole list.
			go func() {
				if err := bootstrapDialogs(ctx, api, cache); err != nil {
					lg.Warn("Re-bootstrap dialogs after difference too long", zap.Error(err))
					return
				}
				lg.Info("Re-bootstrapped dialogs after difference too long")
			}()
		},
		Logger: lg.Named("updates"),
	})

	client, waiter, err := newClient(cfg, mgr, lg, t)
	if err != nil {
		return err
	}

	instrument, err := newMCPInstrument(t)
	if err != nil {
		return err
	}

	return waiter.Run(ctx, func(ctx context.Context) error {
		return client.Run(ctx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return errors.Wrap(err, "auth status")
			}
			if !status.Authorized {
				return errors.New("not authorized: run `tgmcp auth` first to create a session")
			}

			self, err := client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "self")
			}

			// Publish the API for the OnChannelTooLong callback. Set before the
			// updates manager starts, so it is visible by the time updates flow.
			api = client.API()

			srv := &server{
				api:              client.API(),
				cache:            cache,
				msgs:             msgs,
				lg:               lg,
				fileRootVal:      cfg.FileRoot,
				allowSend:        cfg.AllowSend,
				allowProfileEdit: cfg.AllowProfileEdit,
				allowInlineMedia: cfg.AllowInlineMedia,
				attribution:      cfg.Attribution,
				strict:           cfg.Strict,
				footer:           cfg.AgentFooter,
				botToken:         cfg.BotToken,
				botUsername:      cfg.BotUsername,
				sessionDir:       cfg.SessionDir,
				spool:            newInlineSpool(cfg.SessionDir),
			}
			m := mcp.NewServer(&mcp.Implementation{
				Name:    "tgmcp",
				Version: "0.1.0",
			}, nil)
			srv.register(m)
			m.AddReceivingMiddleware(instrument.Middleware())

			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
				return m
			}, nil)
			httpSrv := &http.Server{
				Addr:              cfg.HTTPAddr,
				Handler:           instrumentHTTP(lg, t, handler),
				ReadHeaderTimeout: 10 * time.Second,
			}

			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpSrv.Shutdown(shutdownCtx); err != nil {
					lg.Error("Shutdown MCP HTTP server", zap.Error(err))
				}
			}()

			g, ctx := errgroup.WithContext(ctx)

			// Run the updates manager: it loads the persisted state, seeds the
			// dialog cache once via OnStart, then keeps it live, recovering gaps
			// with getDifference.
			g.Go(func() error {
				return mgr.Run(ctx, client.API(), self.ID, updates.AuthOptions{
					OnStart: func(ctx context.Context) {
						// Seed the cache from persistent storage. Only fetch the
						// full dialog list when nothing is persisted (first run);
						// on later starts the updates manager reconciles the
						// persisted cache via getDifference.
						n, err := cache.loadFromStore()
						if err != nil {
							lg.Error("Load persisted dialogs", zap.Error(err))
						}
						if n == 0 {
							if err := bootstrapDialogs(ctx, client.API(), cache); err != nil {
								lg.Error("Bootstrap dialogs", zap.Error(err))
								return
							}
						} else {
							lg.Info("Loaded persisted dialogs", zap.Int("count", n))
						}
						lg.Info("Authorized, serving MCP over HTTP", zap.String("addr", cfg.HTTPAddr))
					},
				})
			})

			g.Go(func() error {
				if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					return errors.Wrap(err, "http serve")
				}

				return nil
			})

			return g.Wait()
		})
	})
}
