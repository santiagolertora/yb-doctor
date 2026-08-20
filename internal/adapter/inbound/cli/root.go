// Package cli is the inbound adapter for the yb-doctor command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/santiagolertora/yb-doctor/internal/adapter/inbound/cli/render"
	"github.com/santiagolertora/yb-doctor/internal/adapter/outbound/fixture"
	"github.com/santiagolertora/yb-doctor/internal/adapter/outbound/yugabyte"
	"github.com/santiagolertora/yb-doctor/internal/app"
	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
	"github.com/santiagolertora/yb-doctor/internal/observability"
)

// Deps are process-level dependencies injected from main.
type Deps struct {
	Version   string
	Stdout    io.Writer
	Stderr    io.Writer
	Args      []string
	LookupEnv func(string) string
}

// Execute runs the CLI and returns a process exit code.
func Execute(ctx context.Context, deps Deps) int {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.LookupEnv == nil {
		deps.LookupEnv = os.Getenv
	}
	root := newRoot(ctx, deps)
	root.SetArgs(deps.Args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "error: %s\n", err)
		if errors.Is(err, errUsage) {
			return 2
		}
		return 1
	}
	return 0
}

var errUsage = errors.New("cli: usage")

type runtimeFlags struct {
	configFile    string
	masters       string
	scenario      string
	format        string
	noColor       bool
	logLevel      string
	criteria      string
	ysql          string
	ysqlUser      string
	ysqlPassword  string
	ysqlDB        string
	ysqlSSLMode   string
	diffFile      string
	outFile       string
	watch         time.Duration
	watchInterval time.Duration
}

func newRoot(ctx context.Context, deps Deps) *cobra.Command {
	flags := &runtimeFlags{}
	root := &cobra.Command{
		Use:           "yb-doctor",
		Short:         "Diagnose YugabyteDB tablets, Raft, and POC health",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&flags.configFile, "config", "", "TOML config file (default ./yb-doctor.toml if present)")
	root.PersistentFlags().StringVar(&flags.masters, "master", "", "comma-separated YB-Master HTTP addresses (host:7000)")
	root.PersistentFlags().StringVar(&flags.scenario, "scenario", "", "path to a scenario directory or JSON file")
	root.PersistentFlags().StringVar(&flags.format, "format", "", "text or json")
	root.PersistentFlags().BoolVar(&flags.noColor, "no-color", false, "disable ANSI color")
	root.PersistentFlags().StringVar(&flags.logLevel, "log-level", "", "debug|info|warn|error")
	root.PersistentFlags().StringVar(&flags.criteria, "criteria", "", "POC criteria JSON file")
	root.PersistentFlags().StringVar(&flags.ysql, "ysql", "", "YSQL address host:5433")
	root.PersistentFlags().StringVar(&flags.ysqlUser, "ysql-user", "", "YSQL user (default yugabyte)")
	root.PersistentFlags().StringVar(&flags.ysqlPassword, "ysql-password", "", "YSQL password (prefer a TOML file or YB_DOCTOR_YSQL_PASSWORD)")
	root.PersistentFlags().StringVar(&flags.ysqlDB, "ysql-db", "", "YSQL database (default yugabyte)")
	root.PersistentFlags().StringVar(&flags.ysqlSSLMode, "ysql-sslmode", "", "disable|require|verify-ca|verify-full")

	root.AddCommand(analyzeCmd(ctx, deps, flags))
	root.AddCommand(resilienceCmd(ctx, deps, flags))
	root.AddCommand(explainCmd(ctx, deps, flags))
	root.AddCommand(pocCmd(ctx, deps, flags))
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print yb-doctor version",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "yb-doctor %s\n", deps.Version)
		},
	})
	return root
}

func analyzeCmd(ctx context.Context, deps Deps, flags *runtimeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Diagnose topology, tablets, Raft, and performance",
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, cfg, err := buildService(ctx, deps, flags)
			if err != nil {
				return err
			}
			var report *domain.HealthReport
			if cfg.WatchDuration > 0 {
				wctx, cancel := context.WithTimeout(ctx, cfg.WatchDuration)
				defer cancel()
				n := 0
				report, err = svc.WatchAnalyze(wctx, cfg.WatchInterval, func(r *domain.HealthReport) error {
					n++
					if n > 1 {
						_, _ = fmt.Fprintf(deps.Stdout, "\n--- sample %d ---\n", n)
					}
					return render.Health(deps.Stdout, r, renderOpts(cfg))
				})
			} else {
				report, err = svc.Analyze(ctx)
			}
			if err != nil {
				return err
			}
			if cfg.DiffFile != "" {
				prev, err := loadHealthReport(cfg.DiffFile)
				if err != nil {
					return err
				}
				report.Diff = app.DiffReports(cfg.DiffFile, prev, report)
			}
			switch {
			case cfg.WatchDuration > 0 && report.Diff != nil && cfg.DiffFile == "":
				if err := render.Changes(deps.Stdout, report.Diff, renderOpts(cfg)); err != nil {
					return fmt.Errorf("render: %w", err)
				}
			case cfg.WatchDuration == 0:
				if err := render.Health(deps.Stdout, report, renderOpts(cfg)); err != nil {
					return fmt.Errorf("render: %w", err)
				}
			case cfg.DiffFile != "":
				if err := render.Changes(deps.Stdout, report.Diff, renderOpts(cfg)); err != nil {
					return fmt.Errorf("render: %w", err)
				}
			}
			if cfg.OutFile != "" {
				if err := writeHealthReport(cfg.OutFile, report); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.diffFile, "diff", "", "previous analyze --format json file to compare")
	cmd.Flags().StringVar(&flags.outFile, "out", "", "write this report as JSON (for a later --diff)")
	cmd.Flags().DurationVar(&flags.watch, "watch", 0, "re-collect until this duration elapses (e.g. 30s)")
	cmd.Flags().DurationVar(&flags.watchInterval, "watch-interval", 0, "interval between --watch samples")
	return cmd
}

func resilienceCmd(ctx context.Context, deps Deps, flags *runtimeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "resilience",
		Short: "Simulate node, AZ, and region failures against Raft quorum",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cfg, err := buildService(ctx, deps, flags)
			if err != nil {
				return err
			}
			report, err := svc.Resilience(ctx)
			if err != nil {
				return err
			}
			if err := render.Resilience(deps.Stdout, report, renderOpts(cfg)); err != nil {
				return fmt.Errorf("render: %w", err)
			}
			return nil
		},
	}
}

func explainCmd(ctx context.Context, deps Deps, flags *runtimeFlags) *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "explain [finding-code]",
		Short: "Explain a finding",
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				for _, code := range app.KnownFindingCodes() {
					_, _ = fmt.Fprintf(deps.Stdout, "%s\n", code)
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("%w: explain requires a finding code (or --list)", errUsage)
			}
			svc, cfg, err := buildService(ctx, deps, flags)
			if err != nil {
				return err
			}
			exp, err := svc.Explain(ctx, args[0])
			if err != nil {
				return err
			}
			if err := render.Explain(deps.Stdout, exp, renderOpts(cfg)); err != nil {
				return fmt.Errorf("render: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list known finding codes")
	return cmd
}

func pocCmd(ctx context.Context, deps Deps, flags *runtimeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "poc",
		Short: "Generate a YugabyteDB POC readiness report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cfg, err := buildService(ctx, deps, flags)
			if err != nil {
				return err
			}
			criteria := domain.DefaultPOCCriteria()
			path := flags.criteria
			if path == "" {
				path = cfg.CriteriaFile
			}
			if path != "" {
				loaded, err := loadCriteria(path)
				if err != nil {
					return err
				}
				criteria = loaded
			}
			report, err := svc.POC(ctx, criteria)
			if err != nil {
				return err
			}
			if err := render.POC(deps.Stdout, report, renderOpts(cfg)); err != nil {
				return fmt.Errorf("render: %w", err)
			}
			return nil
		},
	}
}

func buildService(ctx context.Context, deps Deps, flags *runtimeFlags) (*app.Service, config.Config, error) {
	path := config.ResolveFile(flags.configFile, deps.LookupEnv)
	cfg, err := config.Load(ctx, config.WithFile(path), config.WithEnv(deps.LookupEnv), withFlags(flags))
	if err != nil {
		return nil, config.Config{}, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, config.Config{}, fmt.Errorf("%w: %w", errUsage, err)
	}
	logger := observability.NewLogger(deps.Stderr, cfg.LogLevel)
	collector, err := newCollector(*cfg, logger)
	if err != nil {
		return nil, config.Config{}, err
	}
	svc, err := app.New(*cfg, collector, logger)
	if err != nil {
		return nil, config.Config{}, fmt.Errorf("app: %w", err)
	}
	return svc, *cfg, nil
}

func withFlags(flags *runtimeFlags) config.Source {
	return func(cfg *config.Config) error {
		if flags.masters != "" {
			cfg.Masters = splitCSV(flags.masters)
		}
		if flags.scenario != "" {
			cfg.Scenario = flags.scenario
		}
		if flags.format != "" {
			cfg.Format = strings.ToLower(flags.format)
		}
		if flags.noColor {
			cfg.NoColor = true
		}
		if flags.logLevel != "" {
			cfg.LogLevel = strings.ToLower(flags.logLevel)
		}
		if flags.criteria != "" {
			cfg.CriteriaFile = flags.criteria
		}
		if flags.ysql != "" {
			cfg.YSQLAddr = flags.ysql
		}
		if flags.ysqlUser != "" {
			cfg.YSQLUser = flags.ysqlUser
		}
		if flags.ysqlPassword != "" {
			cfg.YSQLPassword = flags.ysqlPassword
		}
		if flags.ysqlDB != "" {
			cfg.YSQLDatabase = flags.ysqlDB
		}
		if flags.ysqlSSLMode != "" {
			cfg.YSQLSSLMode = flags.ysqlSSLMode
		}
		if flags.diffFile != "" {
			cfg.DiffFile = flags.diffFile
		}
		if flags.outFile != "" {
			cfg.OutFile = flags.outFile
		}
		if flags.watch > 0 {
			cfg.WatchDuration = flags.watch
		}
		if flags.watchInterval > 0 {
			cfg.WatchInterval = flags.watchInterval
		}
		return nil
	}
}

func newCollector(cfg config.Config, logger *slog.Logger) (app.SnapshotCollector, error) {
	if cfg.Scenario != "" {
		c, err := fixture.New(cfg.Scenario)
		if err != nil {
			return nil, fmt.Errorf("fixture: %w", err)
		}
		return c, nil
	}
	c, err := yugabyte.New(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("yugabyte client: %w", err)
	}
	return c, nil
}

func renderOpts(cfg config.Config) render.Options {
	return render.Options{Format: cfg.Format, NoColor: cfg.NoColor}
}

func loadHealthReport(path string) (*domain.HealthReport, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied --diff flag
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	var r domain.HealthReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode diff file: %w", err)
	}
	return &r, nil
}

func writeHealthReport(path string, r *domain.HealthReport) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func loadCriteria(path string) (domain.POCCriteria, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied --criteria flag
	if err != nil {
		return domain.POCCriteria{}, fmt.Errorf("read criteria: %w", err)
	}
	var c domain.POCCriteria
	if err := json.Unmarshal(raw, &c); err != nil {
		return domain.POCCriteria{}, fmt.Errorf("decode criteria: %w", err)
	}
	return c, nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
