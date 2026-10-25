package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/AlexG-SYS/Test2_ImageProcessing_AlexG_ReeneB/internal/data"
	_ "github.com/lib/pq"
)

type config struct {
	port               int
	env                string
	workerPollInterval time.Duration // how often the worker checks the jobs table
	processingDelay    time.Duration // artificial delay to simulate image processing time
	simDuration        time.Duration // length of the simulated progress phase (0 = off)
	simInterval        time.Duration // time between simulated updates
	storage            struct {
		originalsDir string // where uploaded originals are written
		variantsDir  string // where generated variants are written
	}
	db struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
}

type application struct {
	config        config
	logger        *slog.Logger
	models        data.Models
	wg            sync.WaitGroup
	workerCancel  context.CancelFunc
	notifier      *jobNotifier       // shared job update notification system
	requestCtx    context.Context    // shared request context for graceful shutdown
	cancelRequest context.CancelFunc // cancels the shared request context
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")
	flag.DurationVar(&cfg.workerPollInterval, "worker-poll-interval", 250*time.Millisecond, "Worker queue-check interval")
	flag.DurationVar(&cfg.processingDelay, "processing-delay", 0, "Artificial per-job delay inside the worker (0 = off)")

	// disabled by default so ordinary use is unaffected.
	flag.DurationVar(&cfg.simDuration, "sim-duration", 0, "EXPERIMENT: length of the simulated progress phase before real processing (0 = off, e.g. 10s)")
	flag.DurationVar(&cfg.simInterval, "sim-interval", 2*time.Second, "EXPERIMENT: time between simulated progress updates (e.g. 2s or 200ms)")

	flag.StringVar(&cfg.storage.originalsDir, "storage-originals-dir", "./storage/originals", "Directory for original uploaded images")
	flag.StringVar(&cfg.storage.variantsDir, "storage-variants-dir", "./storage/variants", "Directory for generated image variants")

	flag.StringVar(&cfg.db.dsn, "db-dsn", "postgresql://alex:alex@localhost/imagelab?sslmode=disable", "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 15*time.Minute, "PostgreSQL max connection idle time")

	flag.Parse()

	// fail fast on an unusable simulation configuration.
	if cfg.simDuration > 0 && cfg.simInterval <= 0 {
		fmt.Fprintln(os.Stderr, "-sim-interval must be positive when -sim-duration is set")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	db, err := openDB(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	defer db.Close()

	logger.Info("database connection pool established")

	// request context is cancelled by serve() when shutdown starts.
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()

	app := &application{
		config:        cfg,
		logger:        logger,
		models:        data.NewModels(db),
		notifier:      newJobNotifier(), // [V2]
		requestCtx:    requestCtx,       // [V2]
		cancelRequest: cancelRequest,    // [V2]
	}

	// Log the experiment configuration for visibility.
	logger.Info("experiment_config",
		"processing_delay", cfg.processingDelay.String(),
		"sim_duration", cfg.simDuration.String(),
		"sim_interval", cfg.simInterval.String())

	// The worker gets its own context, cancelled from server.go's shutdown
	// sequence, separate from any per-request context.
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	app.workerCancel = cancelWorker
	defer cancelWorker()
	app.startImageWorker(workerCtx)

	err = app.serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(cfg.db.maxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
