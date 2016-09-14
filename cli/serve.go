package cli

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	kitlog "github.com/go-kit/kit/log"
	"github.com/kolide/kolide-ose/config"
	"github.com/kolide/kolide-ose/datastore"
	"github.com/kolide/kolide-ose/kolide"
	"github.com/kolide/kolide-ose/server"
	"github.com/kolide/kolide-ose/version"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

func createServeCmd(configManager config.Manager) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Launch the kolide server",
		Long: `
Launch the kolide server

Use kolide serve to run the main HTTPS server. The Kolide server bundles
together all static assets and dependent libraries into a statically linked go
binary (which you're executing right now). Use the options below to customize
the way that the kolide server works.
`,
		Run: func(cmd *cobra.Command, args []string) {
			var (
				httpAddr = flag.String("http.addr", ":8080", "HTTP listen address")
				ctx      = context.Background()
				logger   kitlog.Logger
			)
			flag.Parse()

			config := configManager.LoadConfig()

			logger = kitlog.NewLogfmtLogger(os.Stderr)
			logger = kitlog.NewContext(logger).With("ts", kitlog.DefaultTimestampUTC)

			ds, err := datastore.New("inmem", "")
			if err != nil {
				initFatal(err, "initializing datastore")
			}

			svcLogger := kitlog.NewContext(logger).With("component", "service")
			var svc kolide.Service
			{ // temp create an admin user
				svc, _ = server.NewService(ds, logger, config)
				var (
					name     = "admin"
					username = "admin"
					password = "secret"
					email    = "admin@kolide.co"
					enabled  = true
					isAdmin  = true
				)
				admin := kolide.UserPayload{
					Name:     &name,
					Username: &username,
					Password: &password,
					Email:    &email,
					Enabled:  &enabled,
					Admin:    &isAdmin,
				}
				_, err := svc.NewUser(ctx, admin)
				if err != nil {
					initFatal(err, "creating bootstrap user")
				}
				svc = server.NewLoggingService(svc, svcLogger)
			}

			httpLogger := kitlog.NewContext(logger).With("component", "http")

			apiHandler := server.MakeHandler(ctx, svc, config.Auth.JwtKey, ds, httpLogger)
			http.Handle("/api/", accessControl(apiHandler))
			http.Handle("/version", version.Handler())
			http.Handle("/assets/", server.ServeStaticAssets("/assets/"))
			http.Handle("/", server.ServeFrontend())

			errs := make(chan error, 2)
			go func() {
				logger.Log("transport", "http", "address", *httpAddr, "msg", "listening")
				errs <- http.ListenAndServe(*httpAddr, nil)
			}()
			go func() {
				c := make(chan os.Signal)
				signal.Notify(c, syscall.SIGINT)
				errs <- fmt.Errorf("%s", <-c)
			}()

			logger.Log("terminated", <-errs)
		},
	}
}

// cors headers
func accessControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type")

		if r.Method == "OPTIONS" {
			return
		}

		h.ServeHTTP(w, r)
	})
}